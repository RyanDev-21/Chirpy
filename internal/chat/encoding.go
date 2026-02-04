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

func unmarshalBinary(bytesArray []byte)(*chatmodel.MessageMetaData,error){
	var payload *chatmodel.MessageMetaData
	var buff bytes.Buffer
	dec :=gob.NewDecoder(&buff)

	err:=dec.Decode(payload) 
	return payload,err
}
