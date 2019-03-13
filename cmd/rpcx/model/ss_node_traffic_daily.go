package model

import (
	"database/sql"
	"time"

	"github.com/guregu/null"
)

var (
	_ = time.Second
	_ = sql.LevelDefault
	_ = null.Bool{}
)

type SsNodeTrafficDaily struct {
	ID        int         `gorm:"column:id;primary_key" json:"id"`
	NodeID    int         `gorm:"column:node_id" json:"node_id"`
	U         int64       `gorm:"column:u" json:"u"`
	D         int64       `gorm:"column:d" json:"d"`
	Total     int64       `gorm:"column:total" json:"total"`
	Traffic   null.String `gorm:"column:traffic" json:"traffic"`
	CreatedAt null.Time   `gorm:"column:created_at" json:"created_at"`
	UpdatedAt null.Time   `gorm:"column:updated_at" json:"updated_at"`
}

// TableName sets the insert table name for this struct type
func (s *SsNodeTrafficDaily) TableName() string {
	return "ss_node_traffic_daily"
}
