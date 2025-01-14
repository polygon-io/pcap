package pcap

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"time"
)

// FileHeader is the parsed header of a pcap file.
// http://wiki.wireshark.org/Development/LibpcapFileFormat
type FileHeader struct {
	MagicNumber  uint32
	VersionMajor uint16
	VersionMinor uint16
	TimeZone     int32
	SigFigs      uint32
	SnapLen      uint32

	// NOTE: 'Network' property has been changed to `linktype`
	// Please see pcap/pcap.h header file.
	//     Network      uint32
	LinkType uint32
}

// Reader parses pcap files.
type Reader struct {
	flip         bool
	buf          io.Reader
	err          error
	fourBytes    []byte
	twoBytes     []byte
	sixteenBytes []byte
	DataPool     *sync.Pool
	Header       FileHeader
	Count        int
}

type PacketData struct {
	Data []byte
}

func NewPacketData(size int) *PacketData {
	return &PacketData{Data: make([]byte, size)}
}

// NewReader reads pcap data from an io.Reader.
// https://tools.ietf.org/id/draft-gharris-opsawg-pcap-00.html#section-4-5.2.1
func NewReader(reader io.Reader) (r *Reader, err error) {
	r = &Reader{
		buf:          reader,
		fourBytes:    make([]byte, 4),
		twoBytes:     make([]byte, 2),
		sixteenBytes: make([]byte, 16),
	}
	switch magic := r.readUint32(); magic {
	case 0xa1b2c3d4, 0xa1b23c4d:
		r.flip = false
	case 0xd4c3b2a1, 0x4d3cb2a1:
		r.flip = true
	default:
		return nil, fmt.Errorf("pcap: bad magic number: %0x", magic)
	}
	r.Header = FileHeader{
		MagicNumber:  0xa1b23c4d,
		VersionMajor: r.readUint16(),
		VersionMinor: r.readUint16(),
		TimeZone:     r.readInt32(),
		SigFigs:      r.readUint32(),
		SnapLen:      r.readUint32(),
		LinkType:     r.readUint32(),
	}
	r.DataPool = &sync.Pool{
		New: func() interface{} {
			// The Pool's New function should generally only return pointer
			// types, since a pointer can be put into the return interface
			// value without an allocation:
			r.Count++
			return NewPacketData(int(r.Header.SnapLen))
		},
	}
	return r, err
}

// Next returns the next packet or nil if no more packets can be read.
func (r *Reader) Next() *Packet {
	d := r.sixteenBytes
	r.err = r.read(d)
	if r.err != nil {
		return nil
	}
	timeSec := asUint32(d[0:4], r.flip)
	timeUsec := asUint32(d[4:8], r.flip)
	capLen := asUint32(d[8:12], r.flip)
	origLen := asUint32(d[12:16], r.flip)

	packetData := r.DataPool.Get().(*PacketData)
	//fmt.Printf("malloc %p\n", packetData)
	//packetData.Data = packetData.Data[:capLen]
	if r.err = r.read(packetData.Data); r.err != nil {
		return nil
	}
	return &Packet{
		Time:       time.Unix(int64(timeSec), int64(timeUsec)),
		Caplen:     capLen,
		Len:        origLen,
		Data:       packetData.Data,
		PacketData: packetData,
		Pool:       r.DataPool,
	}
}

func (r *Reader) read(data []byte) error {
	var err error
	n, err := r.buf.Read(data)
	for err == nil && n != len(data) {
		var chunk int
		chunk, err = r.buf.Read(data[n:])
		n += chunk
	}
	if len(data) == n {
		return nil
	}
	return err
}

func (r *Reader) readUint32() uint32 {
	data := r.fourBytes
	if r.err = r.read(data); r.err != nil {
		return 0
	}
	return asUint32(data, r.flip)
}

func (r *Reader) readInt32() int32 {
	data := r.fourBytes
	if r.err = r.read(data); r.err != nil {
		return 0
	}
	return int32(asUint32(data, r.flip))
}

func (r *Reader) readUint16() uint16 {
	data := r.twoBytes
	if r.err = r.read(data); r.err != nil {
		return 0
	}
	return asUint16(data, r.flip)
}

// Writer writes a pcap file.
type Writer struct {
	writer io.Writer
	buf    []byte
}

// NewWriter creates a Writer that stores output in an io.Writer.
// The FileHeader is written immediately.
func NewWriter(writer io.Writer, header *FileHeader) (*Writer, error) {
	w := &Writer{
		writer: writer,
		buf:    make([]byte, 24),
	}
	binary.LittleEndian.PutUint32(w.buf, header.MagicNumber)
	binary.LittleEndian.PutUint16(w.buf[4:], header.VersionMajor)
	binary.LittleEndian.PutUint16(w.buf[6:], header.VersionMinor)
	binary.LittleEndian.PutUint32(w.buf[8:], uint32(header.TimeZone))
	binary.LittleEndian.PutUint32(w.buf[12:], header.SigFigs)
	binary.LittleEndian.PutUint32(w.buf[16:], header.SnapLen)
	binary.LittleEndian.PutUint32(w.buf[20:], header.LinkType)
	if _, err := writer.Write(w.buf); err != nil {
		return nil, err
	}
	return w, nil
}

// Writer writes a packet to the underlying writer.
func (w *Writer) Write(pkt *Packet) error {
	binary.LittleEndian.PutUint32(w.buf, uint32(pkt.Time.Unix()))
	binary.LittleEndian.PutUint32(w.buf[4:], uint32(pkt.Time.Nanosecond()))
	binary.LittleEndian.PutUint32(w.buf[8:], pkt.Caplen)
	binary.LittleEndian.PutUint32(w.buf[12:], pkt.Len)
	if _, err := w.writer.Write(w.buf[:16]); err != nil {
		return err
	}
	_, err := w.writer.Write(pkt.Data)
	return err
}

func asUint32(data []byte, flip bool) uint32 {
	if flip {
		return binary.BigEndian.Uint32(data)
	}
	return binary.LittleEndian.Uint32(data)
}

func asUint16(data []byte, flip bool) uint16 {
	if flip {
		return binary.BigEndian.Uint16(data)
	}
	return binary.LittleEndian.Uint16(data)
}
