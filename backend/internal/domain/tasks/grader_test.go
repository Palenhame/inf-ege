package tasks_test

import (
	"testing"

	"backend/internal/domain/tasks"
)

func boolPtr(b bool) *bool {
	return &b
}

func TestGraderSingle(t *testing.T) {
	cfg := tasks.AnswerConfig{
		AnswerType:     "single",
		MaxScore:       1,
		CaseSensitive:  false,
		TrimWhitespace: true,
	}

	correct := tasks.CorrectAnswer{
		AnswerType: "single",
		Accepted:   []string{"12.5", "125/10"},
	}

	tests := []struct {
		name          string
		userAnswer    string
		expectedScore int
		expectedOk    bool
	}{
		{"exact match", "12.5", 1, true},
		{"whitespace trimmed", "  12.5  \n", 1, true},
		{"comma normalized to dot", " 12,5 ", 1, true},
		{"alternative accepted", "125/10", 1, true},
		{"wrong answer", "12.6", 0, false},
		{"empty answer", "", 0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			user := tasks.UserAnswer{
				AnswerType: "single",
				Value:      tc.userAnswer,
			}
			score, isCorrect := tasks.Grade(cfg, correct, user)
			if score != tc.expectedScore {
				t.Errorf("expected score %d, got %d", tc.expectedScore, score)
			}
			if isCorrect != tc.expectedOk {
				t.Errorf("expected isCorrect %v, got %v", tc.expectedOk, isCorrect)
			}
		})
	}
}

func TestGraderSingleCaseSensitivity(t *testing.T) {
	cfgCaseSensitive := tasks.AnswerConfig{
		AnswerType:     "single",
		MaxScore:       1,
		CaseSensitive:  true,
		TrimWhitespace: true,
	}
	correct := tasks.CorrectAnswer{
		AnswerType: "single",
		Accepted:   []string{"KEGE"},
	}

	userLower := tasks.UserAnswer{AnswerType: "single", Value: "kege"}
	score, ok := tasks.Grade(cfgCaseSensitive, correct, userLower)
	if score != 0 || ok {
		t.Errorf("expected 0 false for case sensitive mismatch, got score %d, ok %v", score, ok)
	}

	userExact := tasks.UserAnswer{AnswerType: "single", Value: "KEGE"}
	score, ok = tasks.Grade(cfgCaseSensitive, correct, userExact)
	if score != 1 || !ok {
		t.Errorf("expected 1 true for exact case match, got score %d, ok %v", score, ok)
	}

	// Case insensitive config
	cfgCaseInsensitive := cfgCaseSensitive
	cfgCaseInsensitive.CaseSensitive = false
	score, ok = tasks.Grade(cfgCaseInsensitive, correct, userLower)
	if score != 1 || !ok {
		t.Errorf("expected 1 true for case insensitive match, got score %d, ok %v", score, ok)
	}
}

func TestGraderPair(t *testing.T) {
	t.Run("max_score 2 per_item exact matches", func(t *testing.T) {
		cfg := tasks.AnswerConfig{
			AnswerType:  "pair",
			MaxScore:    2,
			ScoringRule: "per_item",
		}
		correct := tasks.CorrectAnswer{
			AnswerType: "pair",
			Values:     []string{"124", "984512"},
		}

		// Both correct
		u1 := tasks.UserAnswer{AnswerType: "pair", Values: []string{"124", "984512"}}
		score, ok := tasks.Grade(cfg, correct, u1)
		if score != 2 || !ok {
			t.Errorf("both correct: expected 2, true; got %d, %v", score, ok)
		}

		// First correct, second wrong
		u2 := tasks.UserAnswer{AnswerType: "pair", Values: []string{"124", "0"}}
		score, ok = tasks.Grade(cfg, correct, u2)
		if score != 1 || ok {
			t.Errorf("first correct: expected 1, false; got %d, %v", score, ok)
		}

		// Both wrong
		u3 := tasks.UserAnswer{AnswerType: "pair", Values: []string{"0", "0"}}
		score, ok = tasks.Grade(cfg, correct, u3)
		if score != 0 || ok {
			t.Errorf("both wrong: expected 0, false; got %d, %v", score, ok)
		}
	})

	t.Run("allow_swap_for_1pt true awards 1 point on swapped values", func(t *testing.T) {
		cfg := tasks.AnswerConfig{
			AnswerType:      "pair",
			MaxScore:        2,
			ScoringRule:     "per_item",
			AllowSwapFor1Pt: true,
		}
		correct := tasks.CorrectAnswer{
			AnswerType:      "pair",
			Values:          []string{"124", "984512"},
			AllowSwapFor1Pt: true,
		}

		// Swapped values
		uSwapped := tasks.UserAnswer{AnswerType: "pair", Values: []string{"984512", "124"}}
		score, ok := tasks.Grade(cfg, correct, uSwapped)
		if score != 1 || ok {
			t.Errorf("swapped with allow_swap: expected 1, false; got %d, %v", score, ok)
		}

		// Without allow_swap
		cfgNoSwap := cfg
		cfgNoSwap.AllowSwapFor1Pt = false
		correctNoSwap := correct
		correctNoSwap.AllowSwapFor1Pt = false
		score, ok = tasks.Grade(cfgNoSwap, correctNoSwap, uSwapped)
		if score != 0 || ok {
			t.Errorf("swapped without allow_swap: expected 0, false; got %d, %v", score, ok)
		}
	})

	t.Run("scoring_rule exact_only (max_score 1)", func(t *testing.T) {
		cfg := tasks.AnswerConfig{
			AnswerType:  "pair",
			MaxScore:    1,
			ScoringRule: "exact_only",
		}
		correct := tasks.CorrectAnswer{
			AnswerType: "pair",
			Values:     []string{"10", "20"},
		}

		// Both correct
		uBoth := tasks.UserAnswer{AnswerType: "pair", Values: []string{"10", "20"}}
		score, ok := tasks.Grade(cfg, correct, uBoth)
		if score != 1 || !ok {
			t.Errorf("exact_only both match: expected 1, true; got %d, %v", score, ok)
		}

		// One correct -> 0 points under exact_only
		uPartial := tasks.UserAnswer{AnswerType: "pair", Values: []string{"10", "99"}}
		score, ok = tasks.Grade(cfg, correct, uPartial)
		if score != 0 || ok {
			t.Errorf("exact_only partial match: expected 0, false; got %d, %v", score, ok)
		}
	})
}

func TestGraderTable(t *testing.T) {
	cfg := tasks.AnswerConfig{
		AnswerType:              "table",
		MaxScore:                2,
		IgnoreTrailingEmptyRows: boolPtr(true),
		Partial1PtMaxErrors:     1,
		TrimWhitespace:          true,
	}

	correct := tasks.CorrectAnswer{
		AnswerType: "table",
		Matrix: [][]string{
			{"800001", "23"},
			{"800015", "47"},
			{"800045", "101"},
		},
	}

	t.Run("exact match with trailing empty rows ignored", func(t *testing.T) {
		u := tasks.UserAnswer{
			AnswerType: "table",
			Matrix: [][]string{
				{" 800001 ", "23"},
				{"800015", "47"},
				{"800045", "101"},
				{"", ""},
				{"  ", "\t"},
			},
		}
		score, ok := tasks.Grade(cfg, correct, u)
		if score != 2 || !ok {
			t.Errorf("expected 2, true; got %d, %v", score, ok)
		}
	})

	t.Run("1 mismatched row awards partial 1 point", func(t *testing.T) {
		u := tasks.UserAnswer{
			AnswerType: "table",
			Matrix: [][]string{
				{"800001", "23"},
				{"800015", "999"}, // 1 error here
				{"800045", "101"},
			},
		}
		score, ok := tasks.Grade(cfg, correct, u)
		if score != 1 || ok {
			t.Errorf("expected 1, false; got %d, %v", score, ok)
		}
	})

	t.Run("missing last row is 1 error, awards partial 1 point", func(t *testing.T) {
		u := tasks.UserAnswer{
			AnswerType: "table",
			Matrix: [][]string{
				{"800001", "23"},
				{"800015", "47"},
			},
		}
		score, ok := tasks.Grade(cfg, correct, u)
		if score != 1 || ok {
			t.Errorf("expected 1, false; got %d, %v", score, ok)
		}
	})

	t.Run("2 errors exceeds partial limit, awards 0 points", func(t *testing.T) {
		u := tasks.UserAnswer{
			AnswerType: "table",
			Matrix: [][]string{
				{"800001", "23"},
				{"999999", "47"},  // error 1
				{"800045", "999"}, // error 2
			},
		}
		score, ok := tasks.Grade(cfg, correct, u)
		if score != 0 || ok {
			t.Errorf("expected 0, false; got %d, %v", score, ok)
		}
	})
}

func TestGraderTriplet19_21(t *testing.T) {
	cfg := tasks.AnswerConfig{
		AnswerType: "triplet_19_21",
		MaxScore:   3,
	}

	correct := tasks.CorrectAnswer{
		AnswerType: "triplet_19_21",
		Ans19:      []string{"18"},
		Ans20:      []string{"9", "17"},
		Ans21:      []string{"16"},
	}

	t.Run("all three correct scores 3 and is_correct=true", func(t *testing.T) {
		user := tasks.UserAnswer{
			AnswerType: "triplet_19_21",
			Ans19:      "18",
			Ans20:      []string{"9", "17"},
			Ans21:      "16",
		}
		score, ok := tasks.Grade(cfg, correct, user)
		if score != 3 || !ok {
			t.Errorf("expected 3, true; got %d, %v", score, ok)
		}
	})

	t.Run("subtask 20 inverted order still correct", func(t *testing.T) {
		user := tasks.UserAnswer{
			AnswerType: "triplet_19_21",
			Ans19:      "18",
			Ans20:      []string{"17", "9"}, // inverted order
			Ans21:      "16",
		}
		score, ok := tasks.Grade(cfg, correct, user)
		if score != 3 || !ok {
			t.Errorf("expected 3, true; got %d, %v", score, ok)
		}
	})

	t.Run("19 and 21 correct, 20 wrong awards 2 points and is_correct=false", func(t *testing.T) {
		user := tasks.UserAnswer{
			AnswerType: "triplet_19_21",
			Ans19:      "18",
			Ans20:      []string{"5", "10"}, // wrong
			Ans21:      "16",
		}
		score, ok := tasks.Grade(cfg, correct, user)
		if score != 2 {
			t.Errorf("expected score 2, got %d", score)
		}
		if ok {
			t.Errorf("expected is_correct=false when score is 2, got true")
		}
	})

	t.Run("only 19 correct awards 1 point and is_correct=false", func(t *testing.T) {
		user := tasks.UserAnswer{
			AnswerType: "triplet_19_21",
			Ans19:      "18",
			Ans20:      []string{"0", "0"},
			Ans21:      "99",
		}
		score, ok := tasks.Grade(cfg, correct, user)
		if score != 1 || ok {
			t.Errorf("expected 1, false; got %d, %v", score, ok)
		}
	})

	t.Run("all wrong awards 0 points and is_correct=false", func(t *testing.T) {
		user := tasks.UserAnswer{
			AnswerType: "triplet_19_21",
			Ans19:      "99",
			Ans20:      []string{"0", "0"},
			Ans21:      "99",
		}
		score, ok := tasks.Grade(cfg, correct, user)
		if score != 0 || ok {
			t.Errorf("expected 0, false; got %d, %v", score, ok)
		}
	})

	t.Run("handles number types and string representations in ans", func(t *testing.T) {
		user := tasks.UserAnswer{
			AnswerType: "triplet_19_21",
			Ans19:      float64(18),
			Ans20:      "9 17",
			Ans21:      float64(16),
		}
		score, ok := tasks.Grade(cfg, correct, user)
		if score != 3 || !ok {
			t.Errorf("expected 3, true; got %d, %v", score, ok)
		}
	})
}
