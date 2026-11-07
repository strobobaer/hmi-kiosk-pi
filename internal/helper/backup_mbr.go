package helper

import (
	"encoding/binary"
	"errors"
)

const (
	sectorSize   = 512
	mbrTableAt   = 446
	mbrEntrySize = 16
	typeLinux    = 0x83
	// alignSectors: Partition 3 beginnt auf einer Grenze von 1 MiB.
	alignSectors = 2048
)

var errBadPartitionTable = errors.New("Die Partitionstabelle der SD-Karte ist nicht lesbar oder hat nicht die erwarteten Partitionen 1 und 2.")

type mbrEntry struct {
	Type    byte
	Start   uint32
	Sectors uint32
}

func (e mbrEntry) empty() bool { return e.Type == 0 || e.Sectors == 0 }
func (e mbrEntry) end() int64  { return int64(e.Start) + int64(e.Sectors) }

// parseMBR liest die vier Einträge eines MBR-Sektors (Dos-Label).
func parseMBR(sec []byte) ([4]mbrEntry, error) {
	var es [4]mbrEntry
	if len(sec) < sectorSize || sec[510] != 0x55 || sec[511] != 0xaa {
		return es, errBadPartitionTable
	}
	for i := range es {
		b := sec[mbrTableAt+i*mbrEntrySize:]
		es[i] = mbrEntry{
			Type:    b[4],
			Start:   binary.LittleEndian.Uint32(b[8:]),
			Sectors: binary.LittleEndian.Uint32(b[12:]),
		}
	}
	return es, nil
}

// compactMBR liefert eine Kopie des Sektors, in der Partition 3 genau start/sectors
// beschreibt (Linux, LBA) und Partition 4 leer ist. Startcode und Disk-ID bleiben.
func compactMBR(sec []byte, start, sectors int64) []byte {
	out := append([]byte(nil), sec[:sectorSize]...)
	clear(out[mbrTableAt+2*mbrEntrySize : mbrTableAt+4*mbrEntrySize])
	e := out[mbrTableAt+2*mbrEntrySize:]
	// CHS-Felder: 0xFE 0xFF 0xFF heißt "nur LBA gültig".
	e[1], e[2], e[3] = 0xfe, 0xff, 0xff
	e[4] = typeLinux
	e[5], e[6], e[7] = 0xfe, 0xff, 0xff
	binary.LittleEndian.PutUint32(e[8:], uint32(start))
	binary.LittleEndian.PutUint32(e[12:], uint32(sectors))
	return out
}
