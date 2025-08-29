package core

import (
	"bytes"
	"fmt"
	"io"
)

type Packet struct {
	ID      int
	Payload []byte
}

const maxPacketLength = 4096

func ReadPacket(r io.Reader) (Packet, error) {
	var pktLength, pktID VarInt
	var err error

	// read packet length
	_, err = pktLength.ReadFrom(r)
	if err != nil {
		return Packet{}, err
	}

	// read packet id
	n, err := pktID.ReadFrom(r)
	if err != nil {
		return Packet{}, err
	}

	if pktID < 0 {
		return Packet{}, fmt.Errorf("read packet: negateive packet id: %d", pktID)
	}

	if pktLength < 0 || pktLength > maxPacketLength {
		return Packet{}, fmt.Errorf("read packet: invalid packet length: %d", pktLength)
	}

	payloadLen := int32(pktLength) - int32(n)

	if payloadLen < 0 {
		return Packet{}, fmt.Errorf("read packet: invalid payload length: %d", payloadLen)
	}

	packet := Packet{
		ID:      int(pktID),
		Payload: make([]byte, payloadLen),
	}

	// read packet payload
	_, err = io.ReadFull(r, packet.Payload)
	if err != nil {
		return Packet{}, err
	}

	return packet, nil
}

// write a packet
func WritePacket(pktID int, pkt []byte, w io.Writer) error {
	buf := new(bytes.Buffer)

	// length = packet id length + packet length
	pktLength := VarInt(pktID).Len() + len(pkt)

	// write packet length
	_, err := VarInt(pktLength).WriteTo(buf)
	if err != nil {
		return fmt.Errorf("write packet length: %w", err)
	}

	// write packet id
	_, err = VarInt(pktID).WriteTo(buf)
	if err != nil {
		return fmt.Errorf("write packet id: %w", err)
	}

	// write packet
	_, err = buf.Write(pkt)
	if err != nil {
		return fmt.Errorf("write packet to buffer: %w", err)
	}

	// Get the complete packet data
	packetData := buf.Bytes()

	// write to the connection
	n, err := w.Write(packetData)
	if err != nil {
		return fmt.Errorf("write packet to connection: %w", err)
	}

	// Check if we wrote the complete packet
	if n < len(packetData) {
		return fmt.Errorf("short write: wrote %d of %d bytes", n, len(packetData))
	}

	return nil
}

func (p *Packet) Scan(r ...io.ReaderFrom) (int64, error) {
	buf := bytes.NewBuffer(p.Payload)
	n := int64(0)

	for _, v := range r {
		n2, err := v.ReadFrom(buf)
		n += n2
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func Pack(w ...io.WriterTo) ([]byte, error) {
	buf := new(bytes.Buffer)

	// packet payload
	for _, v := range w {
		_, err := v.WriteTo(buf)
		if err != nil {
			return nil, err
		}
	}

	return buf.Bytes(), nil
}
