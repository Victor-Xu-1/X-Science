package mcpstdio

import (
	"context"
	"io"
	"math"
	"synon-go/internal/runtimecontrol"
)

// RPC envelopes retain raw JSON and the SDK may decode it again. Reserve a
// conservative eightfold footprint before materializing the spooled bytes.
// No content is truncated and no successful partial response is invented.
func decodeMCPBuffered(buffer *mcpResponseBuffer, decode func(io.Reader) (rpcMessage, error)) (rpcMessage, error) {
	if err := buffer.ctx.Err(); err != nil {
		return rpcMessage{}, err
	}
	if buffer.size < 0 || uint64(buffer.size) > math.MaxUint64/8 {
		return rpcMessage{}, runtimecontrol.ErrInsufficientMemory
	}
	release, err := runtimecontrol.ReserveDecodeMemory(uint64(buffer.size) * 8)
	if err != nil {
		return rpcMessage{}, err
	}
	defer release()
	reader, err := buffer.reader()
	if err != nil {
		return rpcMessage{}, err
	}
	return decode(mcpDecodeReader{ctx: buffer.ctx, Reader: reader})
}

type mcpDecodeReader struct {
	ctx context.Context
	io.Reader
}

func (r mcpDecodeReader) Read(target []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(target[:min(len(target), mcpResponseBufferBytes)])
}

func decodeMCPEnvelope(reader io.Reader) (rpcMessage, error) {
	var message rpcMessage
	err := decodeMCPJSON(reader, &message)
	return message, err
}
