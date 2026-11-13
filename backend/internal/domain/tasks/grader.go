package tasks

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// TripletItemConfig describes one subtask in a triplet_19_21 card.
type TripletItemConfig struct {
	Kim   int    `json:"kim"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

// AnswerConfig defines grading parameters according to Contract.md §2.2.
type AnswerConfig struct {
	AnswerType              string              `json:"answer_type"`
	MaxScore                int                 `json:"max_score,omitempty"`
	CaseSensitive           bool                `json:"case_sensitive,omitempty"`
	TrimWhitespace          bool                `json:"trim_whitespace,omitempty"`
	Placeholder             string              `json:"placeholder,omitempty"`
	Labels                  []string            `json:"labels,omitempty"`
	ScoringRule             string              `json:"scoring_rule,omitempty"` // "exact_only", "per_item", "per_cell_or_exact"
	AllowSwapFor1Pt         bool                `json:"allow_swap_for_1pt,omitempty"`
	Rows                    int                 `json:"rows,omitempty"`
	Cols                    int                 `json:"cols,omitempty"`
	ColHeaders              []string            `json:"col_headers,omitempty"`
	IgnoreTrailingEmptyRows *bool               `json:"ignore_trailing_empty_rows,omitempty"`
	Partial1PtMaxErrors     int                 `json:"partial_1pt_max_errors,omitempty"`
	Items                   []TripletItemConfig `json:"items,omitempty"`
}

// CorrectAnswer represents the canonical solution structure.
type CorrectAnswer struct {
	AnswerType      string     `json:"answer_type"`
	Accepted        []string   `json:"accepted,omitempty"`
	Values          []string   `json:"values,omitempty"`
	AllowSwapFor1Pt bool       `json:"allow_swap_for_1pt,omitempty"`
	Matrix          [][]string `json:"matrix,omitempty"`
	Ans19           []string   `json:"ans_19,omitempty"`
	Ans20           []string   `json:"ans_20,omitempty"`
	Ans21           []string   `json:"ans_21,omitempty"`
}

// UserAnswer represents student submission.
type UserAnswer struct {
	AnswerType string     `json:"answer_type"`
	Value      string     `json:"value,omitempty"`
	Values     []string   `json:"values,omitempty"`
	Matrix     [][]string `json:"matrix,omitempty"`
	Ans19      any        `json:"ans_19,omitempty"`
	Ans20      any        `json:"ans_20,omitempty"`
	Ans21      any        `json:"ans_21,omitempty"`
}

// GradeResult holds evaluation output.
type GradeResult struct {
	Score     int    `json:"score"`
	MaxScore  int    `json:"max_score"`
	IsCorrect bool   `json:"is_correct"`
	Details   any    `json:"details,omitempty"`
}

// Grade implements the interface contract from PROJECT.md:
// Grade(config AnswerConfig, correct CorrectAnswer, user UserAnswer) (score int, isCorrect bool)
func Grade(config AnswerConfig, correct CorrectAnswer, user UserAnswer) (score int, isCorrect bool) {
	res := GradeAnswer(config, correct, user)
	return res.Score, res.IsCorrect
}

// GradeAnswer evaluates a student's answer against configuration and canonical correct answer.
func GradeAnswer(cfg AnswerConfig, correct CorrectAnswer, user UserAnswer) GradeResult {
	ansType := cfg.AnswerType
	if ansType == "" {
		ansType = correct.AnswerType
	}
	if ansType == "" {
		ansType = user.AnswerType
	}

	// Default whitespace trimming is true unless explicitly configured
	trim := true
	if cfg.TrimWhitespace {
		trim = true
	}

	caseSensitive := cfg.CaseSensitive

	switch ansType {
	case "single":
		return gradeSingle(cfg, correct, user, trim, caseSensitive)
	case "pair":
		return gradePair(cfg, correct, user, trim, caseSensitive)
	case "table":
		return gradeTable(cfg, correct, user, trim, caseSensitive)
	case "triplet_19_21":
		return gradeTriplet(cfg, correct, user, trim, caseSensitive)
	default:
		// Fallback to single if unspecified
		return gradeSingle(cfg, correct, user, trim, caseSensitive)
	}
}

// normalizeToken applies whitespace trimming, case flattening, and decimal comma normalization.
func normalizeToken(s string, trim bool, caseSensitive bool) string {
	if trim {
		s = strings.TrimSpace(s)
	}
	if !caseSensitive {
		s = strings.ToLower(s)
	}
	// Normalize Russian comma decimal separator (e.g. "12,5" -> "12.5")
	s = strings.ReplaceAll(s, ",", ".")
	return s
}

// gradeSingle evaluates single-field answers.
func gradeSingle(cfg AnswerConfig, correct CorrectAnswer, user UserAnswer, trim bool, caseSensitive bool) GradeResult {
	maxScore := cfg.MaxScore
	if maxScore <= 0 {
		maxScore = 1
	}

	userVal := normalizeToken(user.Value, trim, caseSensitive)
	if userVal == "" && len(user.Values) > 0 {
		userVal = normalizeToken(user.Values[0], trim, caseSensitive)
	}

	isMatch := false
	for _, acc := range correct.Accepted {
		normAcc := normalizeToken(acc, trim, caseSensitive)
		if userVal == normAcc {
			isMatch = true
			break
		}
	}

	// If correct.Accepted is empty but correct.Values has an entry, check that too
	if !isMatch && len(correct.Accepted) == 0 && len(correct.Values) > 0 {
		for _, v := range correct.Values {
			if userVal == normalizeToken(v, trim, caseSensitive) {
				isMatch = true
				break
			}
		}
	}

	if isMatch {
		return GradeResult{
			Score:     maxScore,
			MaxScore:  maxScore,
			IsCorrect: true,
		}
	}

	return GradeResult{
		Score:     0,
		MaxScore:  maxScore,
		IsCorrect: false,
	}
}

// gradePair evaluates two-field answers (e.g. №17, 18, 26, 27).
func gradePair(cfg AnswerConfig, correct CorrectAnswer, user UserAnswer, trim bool, caseSensitive bool) GradeResult {
	maxScore := cfg.MaxScore
	if maxScore <= 0 {
		maxScore = 2
	}

	var normCorrect []string
	for _, val := range correct.Values {
		normCorrect = append(normCorrect, normalizeToken(val, trim, caseSensitive))
	}
	for len(normCorrect) < 2 {
		normCorrect = append(normCorrect, "")
	}

	var normUser []string
	for _, val := range user.Values {
		normUser = append(normUser, normalizeToken(val, trim, caseSensitive))
	}
	for len(normUser) < 2 {
		normUser = append(normUser, "")
	}

	allowSwap := correct.AllowSwapFor1Pt || cfg.AllowSwapFor1Pt

	// exact_only rule or maxScore == 1: 1 point only if both match in exact positions
	if cfg.ScoringRule == "exact_only" || maxScore == 1 {
		if normUser[0] == normCorrect[0] && normUser[1] == normCorrect[1] {
			return GradeResult{
				Score:     maxScore,
				MaxScore:  maxScore,
				IsCorrect: true,
			}
		}
		return GradeResult{
			Score:     0,
			MaxScore:  maxScore,
			IsCorrect: false,
		}
	}

	// Default per_item / per_cell_or_exact scoring (maxScore == 2)
	exactMatches := 0
	if normUser[0] != "" && normUser[0] == normCorrect[0] {
		exactMatches++
	}
	if normUser[1] != "" && normUser[1] == normCorrect[1] {
		exactMatches++
	}

	if exactMatches == 2 {
		return GradeResult{
			Score:     2,
			MaxScore:  maxScore,
			IsCorrect: true,
		}
	}

	if exactMatches == 1 {
		return GradeResult{
			Score:     1,
			MaxScore:  maxScore,
			IsCorrect: false,
		}
	}

	// exactMatches == 0, check swapped order
	if allowSwap {
		if normUser[0] != "" && normUser[1] != "" &&
			normUser[0] == normCorrect[1] && normUser[1] == normCorrect[0] {
			return GradeResult{
				Score:     1,
				MaxScore:  maxScore,
				IsCorrect: false,
			}
		}
	}

	return GradeResult{
		Score:     0,
		MaxScore:  maxScore,
		IsCorrect: false,
	}
}

// isRowEmpty returns true if all cells in row are empty or whitespace.
func isRowEmpty(row []string) bool {
	for _, cell := range row {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}

// trimTrailingEmptyRows removes empty rows at the end of a matrix.
func trimTrailingEmptyRows(matrix [][]string) [][]string {
	lastNonEmpty := -1
	for i := len(matrix) - 1; i >= 0; i-- {
		if !isRowEmpty(matrix[i]) {
			lastNonEmpty = i
			break
		}
	}
	if lastNonEmpty == -1 {
		return [][]string{}
	}
	return matrix[:lastNonEmpty+1]
}

// gradeTable evaluates 2D matrix answers (e.g. №25).
func gradeTable(cfg AnswerConfig, correct CorrectAnswer, user UserAnswer, trim bool, caseSensitive bool) GradeResult {
	maxScore := cfg.MaxScore
	if maxScore <= 0 {
		maxScore = 2
	}

	ignoreTrailing := true
	if cfg.IgnoreTrailingEmptyRows != nil {
		ignoreTrailing = *cfg.IgnoreTrailingEmptyRows
	}

	userMatrix := user.Matrix
	correctMatrix := correct.Matrix

	if ignoreTrailing {
		userMatrix = trimTrailingEmptyRows(userMatrix)
		correctMatrix = trimTrailingEmptyRows(correctMatrix)
	}

	userLen := len(userMatrix)
	correctLen := len(correctMatrix)
	maxLen := userLen
	if correctLen > maxLen {
		maxLen = correctLen
	}

	mismatchedRows := 0
	for i := 0; i < maxLen; i++ {
		if i >= userLen || i >= correctLen {
			mismatchedRows++
			continue
		}

		uRow := userMatrix[i]
		cRow := correctMatrix[i]

		if len(uRow) != len(cRow) {
			mismatchedRows++
			continue
		}

		rowMatch := true
		for j := 0; j < len(cRow); j++ {
			uCell := normalizeToken(uRow[j], trim, caseSensitive)
			cCell := normalizeToken(cRow[j], trim, caseSensitive)
			if uCell != cCell {
				rowMatch = false
				break
			}
		}
		if !rowMatch {
			mismatchedRows++
		}
	}

	if mismatchedRows == 0 {
		return GradeResult{
			Score:     maxScore,
			MaxScore:  maxScore,
			IsCorrect: true,
		}
	}

	if cfg.Partial1PtMaxErrors > 0 && mismatchedRows <= cfg.Partial1PtMaxErrors {
		return GradeResult{
			Score:     1,
			MaxScore:  maxScore,
			IsCorrect: false,
		}
	}

	return GradeResult{
		Score:     0,
		MaxScore:  maxScore,
		IsCorrect: false,
	}
}

// extractTokens converts various JSON answer shapes into a normalized string slice.
func extractTokens(v any, trim bool, caseSensitive bool) []string {
	if v == nil {
		return nil
	}

	switch val := v.(type) {
	case string:
		tokens := strings.Fields(strings.ReplaceAll(val, ",", " "))
		var res []string
		for _, tok := range tokens {
			norm := normalizeToken(tok, trim, caseSensitive)
			if norm != "" {
				res = append(res, norm)
			}
		}
		if len(res) == 0 {
			norm := normalizeToken(val, trim, caseSensitive)
			if norm != "" {
				res = append(res, norm)
			}
		}
		return res
	case []string:
		var res []string
		for _, s := range val {
			norm := normalizeToken(s, trim, caseSensitive)
			if norm != "" {
				res = append(res, norm)
			}
		}
		return res
	case []any:
		var res []string
		for _, item := range val {
			s := fmt.Sprintf("%v", item)
			norm := normalizeToken(s, trim, caseSensitive)
			if norm != "" {
				res = append(res, norm)
			}
		}
		return res
	case float64:
		return []string{fmt.Sprintf("%.0f", val)}
	default:
		s := fmt.Sprintf("%v", v)
		norm := normalizeToken(s, trim, caseSensitive)
		if norm != "" {
			return []string{norm}
		}
		return nil
	}
}

// gradeTriplet evaluates №19, 20, 21 subtasks in a single task card.
func gradeTriplet(cfg AnswerConfig, correct CorrectAnswer, user UserAnswer, trim bool, caseSensitive bool) GradeResult {
	maxScore := 3

	// Grade 19 (1 pt)
	score19 := 0
	user19Tokens := extractTokens(user.Ans19, trim, caseSensitive)
	for _, userTok := range user19Tokens {
		matched := false
		for _, acc := range correct.Ans19 {
			if userTok == normalizeToken(acc, trim, caseSensitive) {
				matched = true
				break
			}
		}
		if matched {
			score19 = 1
			break
		}
	}

	// Grade 20 (1 pt) - compares two numbers (sorted or exact order)
	score20 := 0
	user20Tokens := extractTokens(user.Ans20, trim, caseSensitive)
	var correct20Tokens []string
	for _, acc := range correct.Ans20 {
		correct20Tokens = append(correct20Tokens, normalizeToken(acc, trim, caseSensitive))
	}

	if len(user20Tokens) == len(correct20Tokens) && len(user20Tokens) > 0 {
		// Exact comparison
		exact := true
		for i := range user20Tokens {
			if user20Tokens[i] != correct20Tokens[i] {
				exact = false
				break
			}
		}
		if exact {
			score20 = 1
		} else {
			// Compare sorted order so order inversion (e.g. [17, 9] vs [9, 17]) still passes
			sortedUser := append([]string(nil), user20Tokens...)
			sortedCorrect := append([]string(nil), correct20Tokens...)
			sort.Strings(sortedUser)
			sort.Strings(sortedCorrect)

			sortedMatch := true
			for i := range sortedUser {
				if sortedUser[i] != sortedCorrect[i] {
					sortedMatch = false
					break
				}
			}
			if sortedMatch {
				score20 = 1
			}
		}
	}

	// Grade 21 (1 pt)
	score21 := 0
	user21Tokens := extractTokens(user.Ans21, trim, caseSensitive)
	for _, userTok := range user21Tokens {
		matched := false
		for _, acc := range correct.Ans21 {
			if userTok == normalizeToken(acc, trim, caseSensitive) {
				matched = true
				break
			}
		}
		if matched {
			score21 = 1
			break
		}
	}

	totalScore := score19 + score20 + score21
	isCorrect := (totalScore == 3)

	return GradeResult{
		Score:     totalScore,
		MaxScore:  maxScore,
		IsCorrect: isCorrect,
		Details: map[string]int{
			"score_19": score19,
			"score_20": score20,
			"score_21": score21,
		},
	}
}

// UnmarshalJSONHelper parses raw JSON into UserAnswer safely.
func ParseUserAnswer(data []byte) (UserAnswer, error) {
	var ua UserAnswer
	if err := json.Unmarshal(data, &ua); err != nil {
		return ua, err
	}
	return ua, nil
}
