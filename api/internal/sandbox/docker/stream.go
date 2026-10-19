package docker

import (
	"encoding/binary"
	"fmt"
	"io"
)

// demux splits the Engine API's multiplexed stream (8-byte frame headers:
// stream type, three zero bytes, big-endian payload length) into stdout and
// stderr. A nil writer discards that stream.
func demux(source io.Reader, stdout, stderr io.Writer) error {
	var header [8]byte
	for {
		if _, err := io.ReadFull(source, header[:]); err != nil {
			if err == io.EOF {
				return nil
			}
			if err == io.ErrUnexpectedEOF {
				return fmt.Errorf("docker: truncated stream frame")
			}
			return err
		}
		size := int64(binary.BigEndian.Uint32(header[4:]))
		var target io.Writer
		switch header[0] {
		case 0, 1:
			target = stdout
		case 2:
			target = stderr
		default:
			return fmt.Errorf("docker: unknown stream type %d", header[0])
		}
		if target == nil {
			target = io.Discard
		}
		if _, err := io.CopyN(target, source, size); err != nil {
			return err
		}
	}
}

// demuxed returns a reader of the combined stdout and stderr payloads.
func demuxed(source io.ReadCloser) io.ReadCloser {
	reader, writer := io.Pipe()
	go func() {
		err := demux(source, writer, writer)
		_ = source.Close()
		_ = writer.CloseWithError(err)
	}()
	return &pipeCloser{PipeReader: reader, source: source}
}

type pipeCloser struct {
	*io.PipeReader
	source io.Closer
}

func (p *pipeCloser) Close() error {
	_ = p.source.Close()
	return p.PipeReader.Close()
}
