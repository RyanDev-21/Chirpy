package chat

import (
	"bytes"
	"encoding/gob"
)

func marshallBinary(payload interface{}) ([]byte, error) {
	var buff bytes.Buffer
	enc := gob.NewEncoder(&buff)
	err := enc.Encode(payload)

	return buff.Bytes(), err
}

func unmarshalBinary(bytesArray []byte) (*MessageCache, error) {
	var payload MessageCache
	buff := bytes.NewBuffer(bytesArray)
	dec := gob.NewDecoder(buff)

	err := dec.Decode(&payload)
	return &payload, err
}
