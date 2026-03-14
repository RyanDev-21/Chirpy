package chat

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// for somehow reason chaning the driver to the pgv4 version is making the type pgtype
// so need to convert that
func GetStringType(val string) *pgtype.Text {
	return &pgtype.Text{
		String: val,
		Valid:  true,
	}
}

func GetUUIDType(val any) *pgtype.UUID {
	return &pgtype.UUID{
		Bytes: val.(uuid.UUID),
		Valid: true,
	}
}

func GetTimeStampType(val time.Time)pgtype.Timestamp{
	return pgtype.Timestamp{
		Time: val,
		Valid: true,
	}
}
