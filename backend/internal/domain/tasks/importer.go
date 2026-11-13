package tasks

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
)

const (
	MaxUncompressedZipBytes int64 = 150 * 1024 * 1024 // 150 MB zip bomb protection
	DefaultMediaBucket            = "egeinf-media"
	DefaultMediaWorkers           = 8
)

var (
	ErrZipBombDetected = errors.New("uncompressed zip payload exceeds 150 MB limit")
	ErrCSVNotFound     = errors.New("tasks.csv not found in zip archive")
)

// MediaStorage defines an abstraction for uploading task files to S3/MinIO.
type MediaStorage interface {
	Upload(ctx context.Context, bucket, objectKey string, reader io.Reader, size int64, contentType string) (string, error)
}

// Importer handles CSV and ZIP archive ingestion.
type Importer struct {
	repo    Repository
	storage MediaStorage
	bucket  string
}

// NewImporter creates a new task importer instance.
func NewImporter(repo Repository, storage MediaStorage, bucket string) *Importer {
	if bucket == "" {
		bucket = DefaultMediaBucket
	}
	return &Importer{
		repo:    repo,
		storage: storage,
		bucket:  bucket,
	}
}

type mediaFile struct {
	name string
	data []byte
}

// Import parses and processes an input stream (either CSV or ZIP).
func (imp *Importer) Import(ctx context.Context, reader io.Reader, size int64, isZip bool, dryRun bool) (*ImportReportDTO, error) {
	var csvData []byte
	mediaFiles := make(map[string]mediaFile)

	if isZip {
		zipBytes, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("read zip stream: %w", err)
		}

		zipReader, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
		if err != nil {
			return nil, fmt.Errorf("open zip archive: %w", err)
		}

		var totalUncompressed int64
		for _, f := range zipReader.File {
			totalUncompressed += int64(f.UncompressedSize64)
			if totalUncompressed > MaxUncompressedZipBytes {
				return nil, ErrZipBombDetected
			}

			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("open zip entry %s: %w", f.Name, err)
			}

			buf, err := io.ReadAll(io.LimitReader(rc, MaxUncompressedZipBytes-totalUncompressed+int64(f.UncompressedSize64)))
			_ = rc.Close()
			if err != nil {
				return nil, fmt.Errorf("read zip entry %s: %w", f.Name, err)
			}

			cleanName := filepath.ToSlash(f.Name)
			if cleanName == "tasks.csv" || strings.HasSuffix(cleanName, "/tasks.csv") {
				csvData = buf
			} else if !f.FileInfo().IsDir() {
				mediaFiles[cleanName] = mediaFile{name: cleanName, data: buf}
				trimmed := strings.TrimPrefix(cleanName, "media/")
				mediaFiles[trimmed] = mediaFile{name: cleanName, data: buf}
			}
		}

		if len(csvData) == 0 {
			return nil, ErrCSVNotFound
		}
	} else {
		data, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("read csv stream: %w", err)
		}
		csvData = data
	}

	return imp.processCSV(ctx, csvData, mediaFiles, dryRun)
}

func (imp *Importer) processCSV(ctx context.Context, csvBytes []byte, mediaFiles map[string]mediaFile, dryRun bool) (*ImportReportDTO, error) {
	r := csv.NewReader(bytes.NewReader(csvBytes))
	r.FieldsPerRecord = -1 // Allow variable number of fields if trailing empty

	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse csv: %w", err)
	}

	if len(rows) == 0 {
		return &ImportReportDTO{
			DryRun:    dryRun,
			TotalRows: 0,
			Errors:    []ImportRowError{},
		}, nil
	}

	// Build column index map from header row
	headers := rows[0]
	colIndex := make(map[string]int)
	for i, h := range headers {
		colIndex[strings.TrimSpace(strings.ToLower(h))] = i
	}

	report := &ImportReportDTO{
		DryRun:       dryRun,
		TotalRows:    len(rows) - 1,
		Errors:       []ImportRowError{},
		PreviewTasks: []TaskDTO{},
	}

	// Prepare media upload mappings
	type mediaUploadItem struct {
		originalPath string
		s3Key        string
		url          string
		data         []byte
		contentType  string
	}

	var parsedTasks []*Task
	var previewDTOs []TaskDTO

	imageRegex := regexp.MustCompile(`!\[(.*?)\]\((media\/[^)]+)\)`)

	for rowIdx := 1; rowIdx < len(rows); rowIdx++ {
		row := rows[rowIdx]
		if len(row) == 0 || (len(row) == 1 && strings.TrimSpace(row[0]) == "") {
			continue // skip empty lines
		}

		rowNum := rowIdx + 1
		getField := func(colName string) string {
			if idx, ok := colIndex[colName]; ok && idx < len(row) {
				return strings.TrimSpace(row[idx])
			}
			return ""
		}

		// 1. kim_numbers
		kimRaw := getField("kim_numbers")
		if kimRaw == "" {
			report.Errors = append(report.Errors, ImportRowError{
				RowNumber:    rowNum,
				Column:       "kim_numbers",
				ErrorMessage: "missing required kim_numbers",
			})
			continue
		}
		var kimNumbers []int
		for _, part := range strings.Split(kimRaw, ";") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			val, err := strconv.Atoi(part)
			if err != nil || val < 1 || val > 27 {
				report.Errors = append(report.Errors, ImportRowError{
					RowNumber:    rowNum,
					Column:       "kim_numbers",
					ErrorMessage: fmt.Sprintf("invalid kim number: %s", part),
				})
				break
			}
			kimNumbers = append(kimNumbers, val)
		}
		if len(kimNumbers) != 1 && len(kimNumbers) != 3 {
			report.Errors = append(report.Errors, ImportRowError{
				RowNumber:    rowNum,
				Column:       "kim_numbers",
				ErrorMessage: "kim_numbers cardinality must be 1 or 3",
			})
			continue
		}

		// 2. topic_name
		topicName := getField("topic_name")
		if topicName == "" {
			topicName = fmt.Sprintf("Тема КИМ №%d", kimNumbers[0])
		}

		// 3. difficulty
		diffRaw := getField("difficulty")
		difficulty := 1
		if diffRaw != "" {
			if d, err := strconv.Atoi(diffRaw); err == nil && d >= 1 && d <= 4 {
				difficulty = d
			} else {
				report.Errors = append(report.Errors, ImportRowError{
					RowNumber:    rowNum,
					Column:       "difficulty",
					ErrorMessage: "difficulty must be an integer between 1 and 4",
				})
				continue
			}
		}

		// 4. source
		sourceRaw := getField("source")
		var source *string
		if sourceRaw != "" {
			source = &sourceRaw
		}

		// 5. text_md
		textMD := getField("text_md")
		if textMD == "" {
			report.Errors = append(report.Errors, ImportRowError{
				RowNumber:    rowNum,
				Column:       "text_md",
				ErrorMessage: "text_md cannot be empty",
			})
			continue
		}

		// 6. images
		imagesRaw := getField("images")
		var imagesList []string
		if imagesRaw != "" {
			for _, img := range strings.Split(imagesRaw, ";") {
				img = strings.TrimSpace(img)
				if img != "" {
					imagesList = append(imagesList, img)
				}
			}
		}

		// 7. files
		filesRaw := getField("files")
		var filesList []string
		if filesRaw != "" {
			for _, f := range strings.Split(filesRaw, ";") {
				f = strings.TrimSpace(f)
				if f != "" {
					filesList = append(filesList, f)
				}
			}
		}

		// 8. answer_type
		answerType := getField("answer_type")
		if answerType == "" {
			answerType = "single"
		}
		switch answerType {
		case "single", "pair", "table", "triplet_19_21":
		default:
			report.Errors = append(report.Errors, ImportRowError{
				RowNumber:    rowNum,
				Column:       "answer_type",
				ErrorMessage: fmt.Sprintf("unsupported answer_type: %s", answerType),
			})
			continue
		}

		// 9. answer_config_json
		cfgRaw := getField("answer_config_json")
		var answerCfg AnswerConfig
		if cfgRaw != "" {
			if err := json.Unmarshal([]byte(cfgRaw), &answerCfg); err != nil {
				report.Errors = append(report.Errors, ImportRowError{
					RowNumber:    rowNum,
					Column:       "answer_config_json",
					ErrorMessage: fmt.Sprintf("invalid json in answer_config_json: %v", err),
				})
				continue
			}
		}
		if answerCfg.AnswerType == "" {
			answerCfg.AnswerType = answerType
		}
		if answerCfg.MaxScore <= 0 {
			switch answerType {
			case "single":
				answerCfg.MaxScore = 1
			case "pair":
				answerCfg.MaxScore = 2
			case "table":
				answerCfg.MaxScore = 2
			case "triplet_19_21":
				answerCfg.MaxScore = 3
			}
		}

		// 10. correct_answer_json
		correctRaw := getField("correct_answer_json")
		if correctRaw == "" {
			report.Errors = append(report.Errors, ImportRowError{
				RowNumber:    rowNum,
				Column:       "correct_answer_json",
				ErrorMessage: "correct_answer_json cannot be empty",
			})
			continue
		}
		var correctAns CorrectAnswer
		if strings.HasPrefix(correctRaw, "{") {
			if err := json.Unmarshal([]byte(correctRaw), &correctAns); err != nil {
				report.Errors = append(report.Errors, ImportRowError{
					RowNumber:    rowNum,
					Column:       "correct_answer_json",
					ErrorMessage: fmt.Sprintf("invalid json in correct_answer_json: %v", err),
				})
				continue
			}
		} else {
			// Raw single answer string fallback
			correctAns = CorrectAnswer{
				AnswerType: "single",
				Accepted:   []string{correctRaw},
			}
		}
		if correctAns.AnswerType == "" {
			correctAns.AnswerType = answerType
		}

		// 11. solution_md & solution_code
		solMD := getField("solution_md")
		var solutionMD *string
		if solMD != "" {
			solutionMD = &solMD
		}
		solCode := getField("solution_code")
		var solutionCode *string
		if solCode != "" {
			solutionCode = &solCode
		}

		// Replace embedded media paths in text_md
		matches := imageRegex.FindAllStringSubmatch(textMD, -1)
		for _, m := range matches {
			alt := m[1]
			relPath := m[2]
			trimmed := strings.TrimPrefix(relPath, "media/")
			ext := filepath.Ext(relPath)
			if ext == "" {
				ext = ".png"
			}
			newKey := fmt.Sprintf("tasks/images/%s%s", uuid.New().String(), ext)
			newURL := "/storage/" + newKey

			if _, ok := mediaFiles[relPath]; ok {
				textMD = strings.ReplaceAll(textMD, fmt.Sprintf("![%s](%s)", alt, relPath), fmt.Sprintf("![%s](%s)", alt, newURL))
			} else if _, ok := mediaFiles[trimmed]; ok {
				textMD = strings.ReplaceAll(textMD, fmt.Sprintf("![%s](%s)", alt, relPath), fmt.Sprintf("![%s](%s)", alt, newURL))
			}
		}

		// Prepare FileAttachment objects
		var attachments []FileAttachment
		for i, fPath := range filesList {
			cleanName := filepath.Base(fPath)
			ext := strings.TrimPrefix(filepath.Ext(cleanName), ".")
			var sizeBytes int64 = 0
			if mf, ok := mediaFiles[fPath]; ok {
				sizeBytes = int64(len(mf.data))
			} else if mf, ok := mediaFiles[strings.TrimPrefix(fPath, "media/")]; ok {
				sizeBytes = int64(len(mf.data))
			}
			fileUUID := uuid.New().String()
			fileURL := fmt.Sprintf("/storage/tasks/files/%s/%s", fileUUID, cleanName)

			attachments = append(attachments, FileAttachment{
				ID:        fmt.Sprintf("f%d", i+1),
				Name:      cleanName,
				URL:       fileURL,
				SizeBytes: sizeBytes,
				Ext:       ext,
			})
		}

		t := &Task{
			IsPublicBank:  true,
			KimNumbers:    kimNumbers,
			Difficulty:    difficulty,
			Source:        source,
			TextMD:        textMD,
			Images:        imagesList,
			Files:         attachments,
			AnswerConfig:  answerCfg,
			CorrectAnswer: correctAns,
			SolutionMD:    solutionMD,
			SolutionCode:  solutionCode,
		}

		parsedTasks = append(parsedTasks, t)

		previewDTOs = append(previewDTOs, TaskDTO{
			ID:           rowNum,
			IsPublicBank: true,
			KimNumbers:   kimNumbers,
			Topic: TopicDTO{
				KimNumber: kimNumbers[0],
				Name:      topicName,
			},
			Difficulty:   difficulty,
			Source:       source,
			TextMD:       textMD,
			Images:       imagesList,
			Files:        attachments,
			AnswerConfig: answerCfg,
		})
	}

	report.ValidCount = len(parsedTasks)
	report.ErrorCount = len(report.Errors)

	if dryRun {
		report.PreviewTasks = previewDTOs
		return report, nil
	}

	// Real import: upload media files concurrently using worker pool
	if imp.storage != nil && len(mediaFiles) > 0 {
		var wg sync.WaitGroup
		mediaChan := make(chan mediaUploadItem, len(mediaFiles))

		workers := DefaultMediaWorkers
		if len(mediaFiles) < workers {
			workers = len(mediaFiles)
		}

		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for item := range mediaChan {
					contentType := http.DetectContentType(item.data)
					_, _ = imp.storage.Upload(ctx, imp.bucket, item.s3Key, bytes.NewReader(item.data), int64(len(item.data)), contentType)
				}
			}()
		}

		for path, mf := range mediaFiles {
			s3Key := fmt.Sprintf("tasks/media/%s", path)
			mediaChan <- mediaUploadItem{
				originalPath: path,
				s3Key:        s3Key,
				data:         mf.data,
			}
		}
		close(mediaChan)
		wg.Wait()
	}

	// Insert tasks into database
	for i, t := range parsedTasks {
		topicName := previewDTOs[i].Topic.Name
		topicID, err := imp.repo.GetOrCreateTopic(ctx, t.KimNumbers[0], topicName)
		if err != nil {
			report.Errors = append(report.Errors, ImportRowError{
				RowNumber:    i + 2,
				Column:       "topic_name",
				ErrorMessage: fmt.Sprintf("failed to get/create topic: %v", err),
			})
			continue
		}
		t.TopicID = topicID

		newID, err := imp.repo.CreateTask(ctx, t)
		if err != nil {
			report.Errors = append(report.Errors, ImportRowError{
				RowNumber:    i + 2,
				Column:       "task",
				ErrorMessage: fmt.Sprintf("failed to insert task: %v", err),
			})
			continue
		}
		previewDTOs[i].ID = newID
	}

	report.PreviewTasks = previewDTOs
	return report, nil
}
