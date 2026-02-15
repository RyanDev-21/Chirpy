package encoder

import "github.com/google/uuid"

func ConvertToUUID(id []byte) (uuid.UUID, error) {
	uuid, err := uuid.FromBytes(id)
	return uuid, err
}
