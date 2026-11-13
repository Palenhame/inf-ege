package recommendations

// ActiveSessionDTO describes current session progress.
type ActiveSessionDTO struct {
	ID        string  `json:"id"`
	Status    string  `json:"status"`
	StartedAt string  `json:"started_at"`
	ExpiresAt *string `json:"expires_at"`
}

// ActiveAssignmentDTO describes active test or homework assigned to student.
type ActiveAssignmentDTO struct {
	ID         string           `json:"id"`
	RoomID     *string          `json:"room_id"`
	RoomName   string           `json:"room_name"`
	Title      string           `json:"title"`
	Mode       string           `json:"mode"`
	DeadlineAt *string          `json:"deadline_at"`
	Session    ActiveSessionDTO `json:"session"`
}

// KimMatrixCellDTO holds aggregate score stats for KIM 1..27.
type KimMatrixCellDTO struct {
	KimNumber   int     `json:"kim_number"`
	Solved      int     `json:"solved"`
	Attempted   int     `json:"attempted"`
	SuccessRate float64 `json:"success_rate"`
}

// WeakTopicDTO identifies student's problematic topic.
type WeakTopicDTO struct {
	TopicID     int     `json:"topic_id"`
	KimNumber   int     `json:"kim_number"`
	TopicName   string  `json:"topic_name"`
	Score       float64 `json:"score"`
	Solved      int     `json:"solved"`
	Attempted   int     `json:"attempted"`
	SuccessRate float64 `json:"success_rate"`
}

// TaskPreviewDTO is a lightweight task representation for recommendations.
type TaskPreviewDTO struct {
	ID         int    `json:"id"`
	KimNumbers []int  `json:"kim_numbers"`
	Difficulty int    `json:"difficulty"`
	TextMD     string `json:"text_md"`
}

// RecommendedTaskDTO is an item in student recommendations list.
type RecommendedTaskDTO struct {
	RecommendationLogID int64          `json:"recommendation_log_id"`
	Reason              string         `json:"reason"` // "weak_topic" | "adaptive_difficulty"
	Task                TaskPreviewDTO `json:"task"`
}

// StudentDashboardDTO is the complete response for GET /api/v1/dashboard/student.
type StudentDashboardDTO struct {
	ActiveAssignments []ActiveAssignmentDTO `json:"active_assignments"`
	KimMatrix         []KimMatrixCellDTO    `json:"kim_matrix"`
	WeakTopics        []WeakTopicDTO        `json:"weak_topics"`
	RecommendedTasks  []RecommendedTaskDTO  `json:"recommended_tasks"`
}
