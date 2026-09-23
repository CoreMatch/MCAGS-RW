package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
)

const maxPacketSize = 50 * 1024 * 1024

func ReadPacket(r io.Reader) (Packet, error) {
	var header [8]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return Packet{}, err
	}

	length := int(binary.BigEndian.Uint32(header[0:4]))
	packetType := int32(binary.BigEndian.Uint32(header[4:8]))
	if length < 0 || length > maxPacketSize {
		return Packet{}, fmt.Errorf("invalid packet length: %d", length)
	}

	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return Packet{}, err
	}

	return Packet{Type: packetType, Body: body}, nil
}

func WritePacket(w io.Writer, packet Packet) error {
	var header [8]byte
	binary.BigEndian.PutUint32(header[0:4], uint32(len(packet.Body)))
	binary.BigEndian.PutUint32(header[4:8], uint32(packet.Type))

	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	if len(packet.Body) == 0 {
		return nil
	}
	_, err := w.Write(packet.Body)
	return err
}
