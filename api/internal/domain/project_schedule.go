package domain

type ProjectUpdateSchedule struct {
	Mode          string `json:"mode"`
	FrequencyDays int    `json:"frequencyDays"`
	Weekday       int    `json:"weekday"`
	Hour          int    `json:"hour"`
	Timezone      string `json:"timezone"`
}
