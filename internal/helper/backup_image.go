package helper

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"hmikiosk/internal/helperapi"
)

// Das kompakte Abbild: Anfang des Geräts bis Ende von Partition 2 (Boot und System, beide
// schreibgeschützt eingehängt) unverändert, dahinter eine neu gebaute kleine ext4-Datenpartition
// (mkfs.ext4 -d /data in eine Datei unter /run). Auf einer größeren Karte wächst sie beim
// Start (hmi-kiosk-grow-data.service).
const (
	mib = 1 << 20

	// maxDataImage: größer darf die kleine Datenpartition nicht werden (sie liegt im RAM).
	maxDataImage = 1 << 30
	// memReserve bleibt beim Bau im Arbeitsspeicher frei.
	memReserve = 512 * mib
	// dataExtra kommt zu den belegten Bytes plus 25 % hinzu (Wachstum, Metadaten).
	dataExtra   = 64 * mib
	dataLabel   = "HMIDATA"
	fsBlockSize = 4096
	// maxGrowBlocks: bis zu dieser Größe (2 TiB) lässt sich das Dateisystem online vergrößern.
	maxGrowBlocks = (2 << 40) / fsBlockSize

	tempPrefix = "hmi-kiosk-backup-"
)

const msgNotImageSystem = "Die SD-Sicherung gibt es nur für Systeme aus dem SD-Abbild. Für dieses System den Konfigurations-Export nutzen."

// compactPlan sind die berechneten Maße des Abbilds.
type compactPlan struct {
	mbr      []byte // erster Sektor der Quelle
	p2end    int64  // Sektoren
	p3start  int64  // Sektoren, auf 1 MiB ausgerichtet
	dataSize int64  // Bytes der Datenpartition, ein Vielfaches von 1 MiB
	owner    dirAttr
}

func (p *compactPlan) rawSize() int64 { return p.p3start*sectorSize + p.dataSize }

// imageSize ist die Größe der kleinen Datenpartition für used belegte Bytes:
// belegt + 25 % + 64 MiB, auf MiB aufgerundet.
func imageSize(used int64) int64 {
	n := used + used/4 + dataExtra
	return (n + mib - 1) / mib * mib
}

// dataUsed summiert die belegten Bytes unter root (Dateigrößen auf Blöcke aufgerundet,
// Ordner und Verweise je ein Block). Verweise werden nie verfolgt.
func dataUsed(ctx context.Context, root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // zwischen Lesen des Ordners und Anfrage gelöscht
			}
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if p == root {
			return nil // die Wurzel steckt im Zuschlag
		}
		if fi.Mode().IsRegular() {
			total += (fi.Size() + fsBlockSize - 1) / fsBlockSize * fsBlockSize
		} else {
			total += fsBlockSize
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("Die Datenpartition ließ sich nicht ausmessen: %v", err)
	}
	return total, nil
}

func (s *Server) checkImageSystem() error {
	if _, err := os.Stat(s.Backup.imageRelease()); err != nil {
		return errors.New(msgNotImageSystem)
	}
	return nil
}

// cardPath ist das Gerät der SD-Karte: Source (Test) oder das Elterngerät von /boot/firmware.
func (s *Server) cardPath() (path string, mustBeDevice bool, err error) {
	if s.Backup.Source != "" {
		return s.Backup.Source, false, nil
	}
	disk, err := s.bootDisk()
	if err != nil {
		return "", false, err
	}
	return s.Backup.devDir() + "/" + disk, true, nil
}

// planCompact liest die Partitionstabelle, misst /data und prüft die Grenzen. Die Quelle
// bleibt geöffnet zurück; der Aufrufer schließt sie.
func (s *Server) planCompact(ctx context.Context) (*compactPlan, *os.File, error) {
	if err := s.checkImageSystem(); err != nil {
		return nil, nil, err
	}
	path, mustBeDevice, err := s.cardPath()
	if err != nil {
		return nil, nil, err
	}
	dev, err := openCard(path, mustBeDevice)
	if err != nil {
		return nil, nil, err
	}
	p, err := s.measure(ctx, dev)
	if err != nil {
		dev.Close()
		return nil, nil, err
	}
	return p, dev, nil
}

func openCard(path string, mustBeDevice bool) (*os.File, error) {
	fail := func(err error) (*os.File, error) {
		return nil, errors.New("Die SD-Karte ließ sich nicht öffnen: " + err.Error())
	}
	f, err := os.Open(path)
	if err != nil {
		return fail(err)
	}
	fi, err := f.Stat()
	if err == nil && mustBeDevice && fi.Mode()&os.ModeDevice == 0 {
		err = errors.New("kein Blockgerät")
	}
	if err != nil {
		f.Close()
		return fail(err)
	}
	return f, nil
}

func (s *Server) measure(ctx context.Context, dev *os.File) (*compactPlan, error) {
	size, err := dev.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, errors.New("Die SD-Karte ließ sich nicht öffnen: " + err.Error())
	}
	sec := make([]byte, sectorSize)
	if _, err := dev.ReadAt(sec, 0); err != nil {
		return nil, errBadPartitionTable
	}
	es, err := parseMBR(sec)
	if err != nil {
		return nil, err
	}
	p1, p2 := es[0], es[1]
	if p1.empty() || p2.empty() || int64(p2.Start) < p1.end() || p2.end() > size/sectorSize || p2.end() > math.MaxUint32-maxDataImage/sectorSize-alignSectors {
		return nil, errBadPartitionTable
	}

	used, err := dataUsed(ctx, s.Backup.dataDir())
	if err != nil {
		return nil, err
	}
	data := imageSize(used)
	if data > maxDataImage {
		return nil, fmt.Errorf("Die Daten auf der Datenpartition (%d MB belegt) sind zu groß für die kompakte Sicherung (höchstens %d MB Abbild).", used/mib, maxDataImage/mib)
	}
	avail, err := s.Backup.memAvailable()
	if err != nil {
		return nil, err
	}
	if avail < data+memReserve {
		return nil, fmt.Errorf("Im Arbeitsspeicher ist nicht genug Platz für die Sicherung (%d MB nötig, %d MB frei).", (data+memReserve)/mib, avail/mib)
	}
	runDir := s.Backup.runDir()
	free, err := s.Backup.free(runDir)
	if err != nil {
		return nil, fmt.Errorf("Der freie Platz in %s ist nicht lesbar: %v", runDir, err)
	}
	if free < data {
		return nil, fmt.Errorf("In %s ist nicht genug Platz für die Sicherung (%d MB nötig, %d MB frei).", runDir, data/mib, free/mib)
	}
	owner, err := dirAttrOf(s.Backup.dataDir())
	if err != nil {
		return nil, fmt.Errorf("Die Datenpartition ließ sich nicht lesen: %v", err)
	}
	return &compactPlan{
		mbr: sec, p2end: p2.end(), owner: owner, dataSize: data,
		p3start: (p2.end() + alignSectors - 1) / alignSectors * alignSectors,
	}, nil
}

// backupInfo meldet die erwartete Rohgröße oder den Grund, warum keine Sicherung möglich ist.
func (s *Server) backupInfo(ctx context.Context) helperapi.BackupInfo {
	p, dev, err := s.planCompact(ctx)
	if err != nil {
		return helperapi.BackupInfo{Problem: err.Error()}
	}
	dev.Close()
	return helperapi.BackupInfo{RawBytes: p.rawSize()}
}

// openCompact baut das kompakte Abbild und liefert es als Strom mit seiner Gesamtgröße.
// Das Schließen entfernt die Temp-Datei; bei jedem Fehler geschieht das schon hier.
func (s *Server) openCompact(ctx context.Context) (io.ReadCloser, int64, error) {
	p, dev, err := s.planCompact(ctx)
	if err != nil {
		return nil, 0, err
	}
	tmp, err := os.MkdirTemp(s.Backup.runDir(), tempPrefix)
	if err != nil {
		dev.Close()
		return nil, 0, fmt.Errorf("Der Arbeitsordner der Sicherung ließ sich nicht anlegen: %v", err)
	}
	cr := &compactReader{dev: dev, tmp: tmp}
	img, err := s.buildDataImage(ctx, p, filepath.Join(tmp, "data.img"))
	if err != nil {
		cr.Close()
		return nil, 0, err
	}
	cr.img = img
	cr.Reader = io.MultiReader(
		bytes.NewReader(compactMBR(p.mbr, p.p3start, p.dataSize/sectorSize)),
		io.NewSectionReader(dev, sectorSize, p.p2end*sectorSize-sectorSize),
		io.LimitReader(zeroReader{}, (p.p3start-p.p2end)*sectorSize),
		io.LimitReader(img, p.dataSize),
	)
	return cr, p.rawSize(), nil
}

// buildDataImage baut die Datenpartition aus /data in die Datei path und öffnet sie zum Lesen.
func (s *Server) buildDataImage(ctx context.Context, p *compactPlan, path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		err = f.Truncate(p.dataSize)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		return nil, fmt.Errorf("Die Datenpartition für die Sicherung ließ sich nicht anlegen: %v", err)
	}
	// Das Wurzelverzeichnis übernimmt mke2fs nicht aus -d: Besitzer per Option, Rechte danach.
	opts := fmt.Sprintf("root_owner=%d:%d,resize=%d,lazy_itable_init=1,lazy_journal_init=1", p.owner.uid, p.owner.gid, maxGrowBlocks)
	if err := s.exec(ctx, "mkfs.ext4", "-q", "-F", "-L", dataLabel, "-b", strconv.Itoa(fsBlockSize), "-I", "256", "-m", "1",
		"-E", opts, "-d", s.Backup.dataDir(), path); err != nil {
		return nil, fmt.Errorf("Die Datenpartition für die Sicherung ließ sich nicht erstellen: %v", err)
	}
	mode := fmt.Sprintf("set_inode_field <2> mode 0%o", 0o40000|p.owner.perm)
	if err := s.exec(ctx, "debugfs", "-w", "-R", mode, path); err != nil {
		return nil, fmt.Errorf("Die Rechte der Datenpartition ließen sich nicht setzen: %v", err)
	}
	img, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("Die Datenpartition für die Sicherung ließ sich nicht öffnen: %v", err)
	}
	if fi, err := img.Stat(); err != nil || fi.Size() != p.dataSize {
		img.Close()
		return nil, errors.New("Die Datenpartition für die Sicherung hat eine unerwartete Größe.")
	}
	return img, nil
}

type compactReader struct {
	io.Reader
	dev, img *os.File
	tmp      string
	once     sync.Once
}

func (c *compactReader) Close() error {
	c.once.Do(func() {
		if c.dev != nil {
			c.dev.Close()
		}
		if c.img != nil {
			c.img.Close()
		}
		os.RemoveAll(c.tmp)
	})
	return nil
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// memAvailable liest MemAvailable aus meminfo.
func (b BackupConfig) memAvailable() (int64, error) {
	f, err := os.Open(b.or(b.MemInfo, "/proc/meminfo"))
	if err != nil {
		return 0, errors.New("Der freie Arbeitsspeicher ist nicht lesbar: " + err.Error())
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		rest, ok := strings.CutPrefix(sc.Text(), "MemAvailable:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) >= 1 {
			if kb, err := strconv.ParseInt(fields[0], 10, 64); err == nil && kb >= 0 {
				return kb * 1024, nil
			}
		}
	}
	return 0, errors.New("Der freie Arbeitsspeicher ist nicht lesbar.")
}
