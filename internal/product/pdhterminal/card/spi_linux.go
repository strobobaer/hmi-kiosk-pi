//go:build linux

package card

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"unsafe"
)

// ioctl-Nummern aus linux/spi/spidev.h (SPI_IOC_MAGIC 'k').
const (
	spiIocWrMode        = 0x40016B01 // SPI_IOC_WR_MODE (u8)
	spiIocWrBitsPerWord = 0x40016B03 // SPI_IOC_WR_BITS_PER_WORD (u8)
	spiIocWrMaxSpeedHz  = 0x40046B04 // SPI_IOC_WR_MAX_SPEED_HZ (u32)
	spiIocMessage1      = 0x40206B00 // SPI_IOC_MESSAGE(1): sizeof(spi_ioc_transfer) = 32

	spiSpeedHz = 1000000
)

// spiIocTransfer entspricht struct spi_ioc_transfer (32 Byte).
type spiIocTransfer struct {
	txBuf       uint64
	rxBuf       uint64
	length      uint32
	speedHz     uint32
	delayUsecs  uint16
	bitsPerWord uint8
	csChange    uint8
	txNbits     uint8
	rxNbits     uint8
	wordDelay   uint8
	pad         uint8
}

type spidev struct{ f *os.File }

// openSPI öffnet ein spidev-Gerät (Modus 0, 8 Bit, 1 MHz).
func openSPI(dev string) (spiBus, error) {
	f, err := os.OpenFile(dev, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	for _, c := range []struct {
		name string
		req  uintptr
		val  uint32
	}{
		{"Modus", spiIocWrMode, 0},
		{"Wortbreite", spiIocWrBitsPerWord, 8},
		{"Takt", spiIocWrMaxSpeedHz, spiSpeedHz},
	} {
		v := c.val
		if err := ioctlPtr(f, c.req, unsafe.Pointer(&v)); err != nil {
			f.Close()
			return nil, fmt.Errorf("SPI %s: %w", c.name, err)
		}
	}
	return &spidev{f: f}, nil
}

func (s *spidev) Transfer(tx []byte) ([]byte, error) {
	if len(tx) == 0 {
		return nil, errors.New("SPI: leere Übertragung")
	}
	rx := make([]byte, len(tx))
	xfer := spiIocTransfer{
		txBuf:       uint64(uintptr(unsafe.Pointer(&tx[0]))),
		rxBuf:       uint64(uintptr(unsafe.Pointer(&rx[0]))),
		length:      uint32(len(tx)),
		speedHz:     spiSpeedHz,
		bitsPerWord: 8,
	}
	err := ioctlPtr(s.f, spiIocMessage1, unsafe.Pointer(&xfer))
	runtime.KeepAlive(tx) // die Struktur trägt nur Zahlen; Puffer bis nach dem ioctl am Leben halten
	runtime.KeepAlive(rx)
	if err != nil {
		return nil, err
	}
	return rx, nil
}

func (s *spidev) Close() error { return s.f.Close() }
