package httpx

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
)

const maxBodyBytes = 1 << 20 // 1 MiB

var ErrBadPathParam = errors.New("invalid path parameter")

// Decode reads a JSON body (capped at 1 MiB) into v.
func Decode[T any](w http.ResponseWriter, r *http.Request, v *T) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	return json.NewDecoder(r.Body).Decode(v)
}

// PathUUID parses a UUID path parameter.
func PathUUID(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		return uuid.Nil, ErrBadPathParam
	}
	return id, nil
}
