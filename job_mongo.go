package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"time"
)

// Port-forwards that map to MongoDB need a wire-protocol handshake instead of
// a TCP connect, because kubectl accepts every local connection even when the
// remote database cannot answer.

const (
	mongoWireProtocolPort = 27017
	mongoHelloTimeout     = 2 * time.Second
	mongoHelloMaxBytes    = 64 * 1024
	mongoOPMsgOpcode      = int32(2013)
)

const (
	bsonTypeDouble     = 0x01
	bsonTypeString     = 0x02
	bsonTypeDocument   = 0x03
	bsonTypeArray      = 0x04
	bsonTypeBinary     = 0x05
	bsonTypeUndefined  = 0x06
	bsonTypeObjectID   = 0x07
	bsonTypeBoolean    = 0x08
	bsonTypeDateTime   = 0x09
	bsonTypeNull       = 0x0A
	bsonTypeRegex      = 0x0B
	bsonTypeDBPointer  = 0x0C
	bsonTypeJavaScript = 0x0D
	bsonTypeSymbol     = 0x0E
	bsonTypeCodeScope  = 0x0F
	bsonTypeInt32      = 0x10
	bsonTypeTimestamp  = 0x11
	bsonTypeInt64      = 0x12
	bsonTypeDecimal128 = 0x13
	bsonTypeMinKey     = 0xFF
	bsonTypeMaxKey     = 0x7F
)

// probeMongoHello sends an unauthenticated hello command and reports an error
// unless the server answers with a successful OP_MSG reply.
func probeMongoHello(address string, timeout time.Duration) error {
	connection, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return err
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	if _, err := connection.Write(encodeMongoHelloRequest()); err != nil {
		return err
	}
	return readMongoHelloReply(connection)
}

func encodeMongoHelloRequest() []byte {
	return encodeMongoOPMsg(1, encodeBSONDocument(
		bsonInt32Element("hello", 1),
		bsonStringElement("$db", "admin"),
	))
}

func encodeMongoOPMsg(requestID int32, document []byte) []byte {
	message := make([]byte, 21+len(document))
	binary.LittleEndian.PutUint32(message[0:4], uint32(len(message)))
	binary.LittleEndian.PutUint32(message[4:8], uint32(requestID))
	binary.LittleEndian.PutUint32(message[12:16], uint32(mongoOPMsgOpcode))
	message[20] = 0 // body section
	copy(message[21:], document)
	return message
}

type bsonElement struct {
	kind  byte
	name  string
	value []byte
}

func bsonInt32Element(name string, value int32) bsonElement {
	raw := make([]byte, 4)
	binary.LittleEndian.PutUint32(raw, uint32(value))
	return bsonElement{kind: bsonTypeInt32, name: name, value: raw}
}

func bsonStringElement(name, value string) bsonElement {
	raw := make([]byte, 4+len(value)+1)
	binary.LittleEndian.PutUint32(raw[0:4], uint32(len(value)+1))
	copy(raw[4:], value)
	return bsonElement{kind: bsonTypeString, name: name, value: raw}
}

func encodeBSONDocument(elements ...bsonElement) []byte {
	size := 5
	for _, element := range elements {
		size += 1 + len(element.name) + 1 + len(element.value)
	}
	document := make([]byte, size)
	binary.LittleEndian.PutUint32(document[0:4], uint32(size))
	offset := 4
	for _, element := range elements {
		document[offset] = element.kind
		offset++
		offset += copy(document[offset:], element.name)
		document[offset] = 0
		offset++
		offset += copy(document[offset:], element.value)
	}
	document[size-1] = 0
	return document
}

func readMongoHelloReply(reader io.Reader) error {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return errors.New("MongoDB did not reply to the hello handshake")
	}
	size := int(int32(binary.LittleEndian.Uint32(header[0:4])))
	if size < 21 || size > mongoHelloMaxBytes {
		return fmt.Errorf("MongoDB reply length %d is invalid", size)
	}
	message := make([]byte, size-4)
	if _, err := io.ReadFull(reader, message); err != nil {
		return errors.New("MongoDB reply was incomplete")
	}
	if opcode := int32(binary.LittleEndian.Uint32(message[8:12])); opcode != mongoOPMsgOpcode {
		return fmt.Errorf("MongoDB reply opcode %d is not OP_MSG", opcode)
	}
	bodyEnd := len(message)
	if flags := binary.LittleEndian.Uint32(message[12:16]); flags&1 != 0 {
		bodyEnd -= 4
	}
	if bodyEnd <= 16 {
		return errors.New("MongoDB reply has no body section")
	}
	if message[16] != 0 {
		return errors.New("MongoDB reply does not start with a body section")
	}
	ok, err := bsonNumericField(message[17:bodyEnd], "ok")
	if err != nil {
		return err
	}
	if ok != 1 {
		return fmt.Errorf("MongoDB hello reported ok=%v", ok)
	}
	return nil
}

func bsonNumericField(document []byte, field string) (float64, error) {
	if len(document) < 5 {
		return 0, errors.New("MongoDB reply body is too short")
	}
	declared := int(int32(binary.LittleEndian.Uint32(document[0:4])))
	if declared != len(document) {
		return 0, errors.New("MongoDB reply body length is invalid")
	}
	offset := 4
	for offset < len(document) {
		kind := document[offset]
		offset++
		if kind == 0 {
			break
		}
		name, next, err := bsonCString(document, offset)
		if err != nil {
			return 0, err
		}
		offset = next
		size, err := bsonValueSize(kind, document, offset)
		if err != nil {
			return 0, err
		}
		if name == field {
			switch kind {
			case bsonTypeDouble:
				return math.Float64frombits(binary.LittleEndian.Uint64(document[offset : offset+8])), nil
			case bsonTypeInt32:
				return float64(int32(binary.LittleEndian.Uint32(document[offset : offset+4]))), nil
			case bsonTypeInt64:
				return float64(int64(binary.LittleEndian.Uint64(document[offset : offset+8]))), nil
			case bsonTypeBoolean:
				if document[offset] != 0 {
					return 1, nil
				}
				return 0, nil
			}
			return 0, fmt.Errorf("MongoDB reply %s field has a non-numeric BSON type", field)
		}
		offset += size
	}
	return 0, fmt.Errorf("MongoDB reply has no %s field", field)
}

func bsonCString(document []byte, offset int) (string, int, error) {
	for index := offset; index < len(document); index++ {
		if document[index] == 0 {
			return string(document[offset:index]), index + 1, nil
		}
	}
	return "", 0, errors.New("MongoDB reply contains an unterminated field name")
}

func bsonValueSize(kind byte, document []byte, offset int) (int, error) {
	invalid := errors.New("MongoDB reply contains a malformed BSON value")
	switch kind {
	case bsonTypeDouble:
		return bsonFixedSize(document, offset, 8)
	case bsonTypeString, bsonTypeJavaScript, bsonTypeSymbol:
		if offset+4 > len(document) {
			return 0, invalid
		}
		length := int(int32(binary.LittleEndian.Uint32(document[offset : offset+4])))
		if length < 1 || offset+4+length > len(document) {
			return 0, invalid
		}
		return 4 + length, nil
	case bsonTypeDocument, bsonTypeArray:
		if offset+4 > len(document) {
			return 0, invalid
		}
		length := int(int32(binary.LittleEndian.Uint32(document[offset : offset+4])))
		if length < 5 || offset+length > len(document) {
			return 0, invalid
		}
		return length, nil
	case bsonTypeBinary:
		if offset+5 > len(document) {
			return 0, invalid
		}
		length := int(int32(binary.LittleEndian.Uint32(document[offset : offset+4])))
		if length < 0 || offset+5+length > len(document) {
			return 0, invalid
		}
		return 5 + length, nil
	case bsonTypeUndefined, bsonTypeNull, bsonTypeMinKey, bsonTypeMaxKey:
		return 0, nil
	case bsonTypeObjectID:
		return bsonFixedSize(document, offset, 12)
	case bsonTypeBoolean:
		return bsonFixedSize(document, offset, 1)
	case bsonTypeDateTime, bsonTypeTimestamp:
		return bsonFixedSize(document, offset, 8)
	case bsonTypeRegex:
		_, next, err := bsonCString(document, offset)
		if err != nil {
			return 0, err
		}
		_, next, err = bsonCString(document, next)
		if err != nil {
			return 0, err
		}
		return next - offset, nil
	case bsonTypeDBPointer:
		if offset+4 > len(document) {
			return 0, invalid
		}
		length := int(int32(binary.LittleEndian.Uint32(document[offset : offset+4])))
		if length < 1 || offset+4+length+12 > len(document) {
			return 0, invalid
		}
		return 4 + length + 12, nil
	case bsonTypeCodeScope:
		if offset+4 > len(document) {
			return 0, invalid
		}
		length := int(int32(binary.LittleEndian.Uint32(document[offset : offset+4])))
		if length < 4 || offset+length > len(document) {
			return 0, invalid
		}
		return length, nil
	case bsonTypeInt32:
		return bsonFixedSize(document, offset, 4)
	case bsonTypeInt64:
		return bsonFixedSize(document, offset, 8)
	case bsonTypeDecimal128:
		return bsonFixedSize(document, offset, 16)
	}
	return 0, fmt.Errorf("MongoDB reply contains an unsupported BSON type 0x%02x", kind)
}

func bsonFixedSize(document []byte, offset, size int) (int, error) {
	if offset+size > len(document) {
		return 0, errors.New("MongoDB reply contains a malformed BSON value")
	}
	return size, nil
}
