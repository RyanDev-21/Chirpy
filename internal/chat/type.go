package chat

import (
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

//for somehow reason chaning the driver to the pgv4 version is making the type pgtype
//so need to convert that
func GetStringType(val string)*pgtype.Text{
	return &pgtype.Text{
		String: val,
		Valid: true,
	}
}

func GetUUIDType(val any)*pgtype.UUID{
	return &pgtype.UUID{
		Bytes: val.(uuid.UUID),
		Valid: true,
	}
}


