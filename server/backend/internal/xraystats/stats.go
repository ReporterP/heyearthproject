// Package xraystats предоставляет клиент для Xray Stats API.
// Использует кастомный gRPC codec для работы без зависимости от xray-core.
package xraystats

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// rawCodec — кастомный gRPC codec для работы с raw protobuf bytes.
type rawCodec struct{}

func (rawCodec) Marshal(v interface{}) ([]byte, error) {
	b, ok := v.([]byte)
	if !ok {
		return nil, fmt.Errorf("rawCodec: expected []byte, got %T", v)
	}
	return b, nil
}

func (rawCodec) Unmarshal(data []byte, v interface{}) error {
	bp, ok := v.(*[]byte)
	if !ok {
		return fmt.Errorf("rawCodec: expected *[]byte, got %T", v)
	}
	*bp = append([]byte(nil), data...)
	return nil
}

func (rawCodec) Name() string { return "proto" }

// Stat — одна метрика из ответа Stats API.
type Stat struct {
	Name  string
	Value int64
}

// QueryStats вызывает Xray Stats API и возвращает статистику.
func QueryStats(addr string, pattern string, reset bool) ([]Stat, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	//nolint:staticcheck // ForceCodec is deprecated but ForceCodecV2 requires mem.BufferSlice
	conn, err := grpc.DialContext(ctx, addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(rawCodec{})),
	)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	reqBytes := encodeQueryStatsRequest(pattern, reset)
	var respBytes []byte

	err = conn.Invoke(ctx,
		"/xray.app.stats.command.StatsService/QueryStats",
		reqBytes, &respBytes,
	)
	if err != nil {
		return nil, fmt.Errorf("invoke: %w", err)
	}

	return decodeQueryStatsResponse(respBytes), nil
}

// ── Protobuf wire format encoding/decoding ──

func encodeQueryStatsRequest(pattern string, reset bool) []byte {
	var buf []byte
	if pattern != "" {
		buf = append(buf, 0x0A) // field 1, wire type 2
		buf = appendVarint(buf, uint64(len(pattern)))
		buf = append(buf, []byte(pattern)...)
	}
	if reset {
		buf = append(buf, 0x10, 0x01) // field 2, wire type 0, value 1
	}
	return buf
}

func decodeQueryStatsResponse(data []byte) []Stat {
	var stats []Stat
	pos := 0
	for pos < len(data) {
		fieldTag, n := readVarint(data[pos:])
		pos += n
		if n == 0 {
			break
		}

		fieldNum := fieldTag >> 3
		wireType := fieldTag & 0x07

		if fieldNum == 1 && wireType == 2 {
			// repeated Stat stat = 1
			msgLen, n2 := readVarint(data[pos:])
			pos += n2
			end := pos + int(msgLen)
			if end > len(data) {
				break
			}
			stat := decodeStat(data[pos:end])
			if stat.Name != "" {
				stats = append(stats, stat)
			}
			pos = end
		} else {
			// Пропускаем неизвестные поля
			switch wireType {
			case 0: // varint
				_, n2 := readVarint(data[pos:])
				pos += n2
			case 2: // length-delimited
				length, n2 := readVarint(data[pos:])
				pos += n2 + int(length)
			default:
				return stats
			}
		}
	}
	return stats
}

func decodeStat(data []byte) Stat {
	var stat Stat
	pos := 0
	for pos < len(data) {
		fieldTag, n := readVarint(data[pos:])
		pos += n
		if n == 0 {
			break
		}

		fieldNum := fieldTag >> 3
		wireType := fieldTag & 0x07

		switch {
		case fieldNum == 1 && wireType == 2: // string name = 1
			strLen, n2 := readVarint(data[pos:])
			pos += n2
			end := pos + int(strLen)
			if end <= len(data) {
				stat.Name = string(data[pos:end])
				pos = end
			}
		case fieldNum == 2 && wireType == 0: // int64 value = 2
			val, n2 := readVarint(data[pos:])
			pos += n2
			stat.Value = int64(val)
		default:
			return stat
		}
	}
	return stat
}

func readVarint(data []byte) (uint64, int) {
	var val uint64
	for i := 0; i < len(data) && i < 10; i++ {
		b := data[i]
		val |= uint64(b&0x7F) << (uint(i) * 7)
		if b&0x80 == 0 {
			return val, i + 1
		}
	}
	return 0, 0
}

func appendVarint(buf []byte, v uint64) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v)|0x80)
		v >>= 7
	}
	buf = append(buf, byte(v))
	return buf
}
