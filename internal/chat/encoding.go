package chat

import (
	"bytes"
	"encoding/gob"

	chatmodel "RyanDev-21.com/Chirpy/internal/chatModel"
)
func marshallBinary(payload interface{})([]byte,error){
	var buff bytes.Buffer
	enc:=gob.NewEncoder(&buff)
	err:=enc.Encode(payload)
	
	return buff.Bytes(),err
}

func unmarshalBinary(bytesArray []byte)(*chatmodel.MessageCache,error){
	var payload *chatmodel.MessageCache
	var buff bytes.Buffer
	dec :=gob.NewDecoder(&buff)

	err:=dec.Decode(payload) 
	return payload,err
}
