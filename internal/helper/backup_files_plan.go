package helper

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"hmikiosk/internal/helperapi"
)

// Dateiweise Sicherung: für Systeme ohne SD-Abbild und ohne Schreibschutz (install.sh auf Raspberry Pi OS).
// Der Helfer baut ein neues Abbild mit demselben Layout (Partition 1 FAT für /boot/firmware, Partition 2
// ext4 für /) in einer Arbeitsdatei, formatiert es über ein Loop-Gerät und kopiert die Dateien mit rsync.
const (
	// filesLeadSectors: 4 MiB Vorlauf vor Partition 1.
	filesLeadSectors = 8192
	// filesExtraBytes kommt zu den belegten Bytes plus 25 % hinzu.
	filesExtraBytes = 512 * mib
	// cacheReserve muss auf der Karte zusätzlich zur Arbeitsdatei frei sein (Download).
	cacheReserve = 512 * mib
	// filesInodeRatio: Bytes je Inode des neuen Dateisystems.
	filesInodeRatio = 16384
	fatLabelMax     = 11
)

const msgLayout = "Das Layout dieses Datenträgers (SD-Karte, SSD oder USB-Laufwerk) wird von der dateiweisen Sicherung nicht unterstützt (%s). Unterstützt ist nur das Raspberry-Pi-Layout: Partition 1 (FAT, /boot/firmware) und Partition 2 (ext4, /), sonst nichts."

// filesExcludes sind die Ausschlüsse der Kopie von /, verankert an der Wurzel der Quelle.
// Die Einhängepunkte (proc, sys, dev, run, tmp, mnt, media) legt der Bau danach leer an.
var filesExcludes = []string{
	"/proc", "/sys", "/dev", "/run", "/tmp", "/mnt", "/media", "/lost+found",
	"/var/cache/hmi-kiosk-backup", "/var/swap", "/swapfile", "/var/tmp/*",
}

var filesMountpoints = []struct {
	name string
	mode os.FileMode
}{
	{"proc", 0o755}, {"sys", 0o755}, {"dev", 0o755}, {"run", 0o755}, {"tmp", 0o777 | os.ModeSticky}, {"mnt", 0o755}, {"media", 0o755},
}

// filesTools sind die Programme der dateiweisen Sicherung, soweit sie nicht zur Grundausstattung gehören,
// samt dem Debian-Paket, das sie liefert.
var filesTools = []struct{ name, pkg string }{
	{"rsync", "rsync"}, {"mkfs.vfat", "dosfstools"}, {"losetup", "util-linux"},
}

var (
	diskNameRe = regexp.MustCompile(`^(mmcblk[0-9]+|nvme[0-9]+n[0-9]+|sd[a-z]+)$`)
	fsLabelRe  = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,16}$`)
	fatIDRe    = regexp.MustCompile(`^[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}$`)
	ext4UUIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	loopDevRe  = regexp.MustCompile(`^/dev/loop[0-9]+$`)
)

type fsInfo struct{ label, uuid string }

// filesPlan sind die berechneten Maße und Kennungen des Abbilds.
type filesPlan struct {
	card        string // Gerät der Quellkarte
	sector0     []byte // erster Sektor der Quelle (Startcode, Startkennzeichen)
	oldID       uint32 // Disk-ID der Quelle
	bootSectors int64
	p2Start     int64 // Sektoren, auf 1 MiB ausgerichtet
	sysBytes    int64
	rawSize     int64
	copyBytes   int64 // belegte Bytes von / (Schätzung für den Fortschritt)
	inodes      int64 // 0: -i genügt; sonst -N
	boot, root  fsInfo
}

// usbNeed: Auf dem Stick liegen zugleich die Rohdatei und die komprimierte Datei (gzip streamt aus der Rohdatei, die
// erst danach gelöscht wird). Ein gut gefülltes System komprimiert mit gzip (schnellste Stufe) nur auf etwa 50 bis
// 70 % der Rohgröße; darum Rohgröße + 70 % + 256 MiB Reserve. Die einfachere Alternative, die Rohdatei auf der Karte
// zu bauen, scheidet aus: Dort fehlt oft der Platz, der Stick-Weg soll gerade dann gehen.
func (p *filesPlan) usbNeed() int64 { return p.rawSize + p.rawSize*7/10 + 256*mib }

// filesSystemSize: belegt + 25 % + Zuschlag, auf MiB aufgerundet.
func filesSystemSize(used, extra int64) int64 {
	n := used + used/4 + extra
	return (n + mib - 1) / mib * mib
}

func fmtSize(n int64) string {
	if n >= 1<<30 {
		return strings.Replace(fmt.Sprintf("%.1f GB", float64(n)/(1<<30)), ".", ",", 1)
	}
	return fmt.Sprintf("%d MB", (n+mib-1)/mib)
}

func layoutErr(detail string) error { return fmt.Errorf(msgLayout, detail) }

func checkDiskName(name string) error {
	if !diskNameRe.MatchString(name) {
		return layoutErr("das Gerät ist kein mmcblk-, nvme- oder sd-Datenträger, sondern " + name)
	}
	return nil
}

// checkLayout prüft die Partitionstabelle: genau Partition 1 (FAT) und 2 (Linux) in dieser Reihenfolge.
func checkLayout(es [4]mbrEntry, devSectors int64) error {
	p1, p2 := es[0], es[1]
	if p1.empty() || p2.empty() {
		return layoutErr("Partition 1 oder 2 fehlt")
	}
	if !es[2].empty() || !es[3].empty() || es[2].Type != 0 || es[3].Type != 0 {
		return layoutErr("es gibt weitere Partitionen")
	}
	if p1.Type != 0x0b && p1.Type != 0x0c {
		return layoutErr(fmt.Sprintf("Partition 1 hat den Typ 0x%02x statt FAT32", p1.Type))
	}
	if p2.Type != typeLinux {
		return layoutErr(fmt.Sprintf("Partition 2 hat den Typ 0x%02x statt Linux", p2.Type))
	}
	if int64(p2.Start) < p1.end() || p2.end() > devSectors {
		return layoutErr("die Partitionen liegen nicht in der erwarteten Reihenfolge innerhalb der Karte")
	}
	return nil
}

// mountDev liefert major:minor des zuletzt eingehängten Dateisystems auf dem Einhängepunkt.
func (s *Server) mountDev(point string) (string, bool) {
	raw, err := os.ReadFile(s.Backup.mountInfo())
	if err != nil {
		return "", false
	}
	dev := ""
	for _, line := range strings.Split(string(raw), "\n") {
		lf := strings.Fields(line)
		if len(lf) >= 5 && lf[4] == point {
			dev = lf[2]
		}
	}
	return dev, dev != ""
}

func (s *Server) partDevNum(disk string, n int) string {
	b, err := os.ReadFile(filepath.Join(s.Backup.sysClass(), partName(disk, n), "dev"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// checkMounts prüft über die Gerätenummern, dass / auf Partition 2 und /boot/firmware auf Partition 1
// derselben Karte liegt (gleiche Erkennung wie die SD-Abbild-Sicherung).
func (s *Server) checkMounts(card string) error {
	disk := filepath.Base(card)
	if err := checkDiskName(disk); err != nil {
		return err
	}
	for _, c := range []struct {
		point string
		n     int
	}{{"/", 2}, {s.Backup.bootMount(), 1}} {
		got, ok := s.mountDev(c.point)
		want := s.partDevNum(disk, c.n)
		if !ok || want == "" || got != want {
			return layoutErr(fmt.Sprintf("%s liegt nicht auf Partition %d von %s", c.point, c.n, disk))
		}
	}
	return nil
}

func (s *Server) checkTools() error {
	if s.Backup.HasTool == nil {
		return nil
	}
	var miss, pkgs []string
	for _, t := range filesTools {
		if !s.Backup.HasTool(t.name) {
			miss = append(miss, t.name)
			pkgs = append(pkgs, t.pkg)
		}
	}
	if len(miss) == 0 {
		return nil
	}
	return fmt.Errorf("Für die dateiweise Sicherung fehlen Programme: %s. Bitte nachinstallieren: sudo apt install %s", strings.Join(miss, ", "), strings.Join(pkgs, " "))
}

// parseExport liest die Ausgabe von blkid -o export (Zeilen SCHLÜSSEL=Wert).
func parseExport(out string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			m[k] = v
		}
	}
	return m
}

// probeFS liest Typ, Beschriftung und Kennung eines Dateisystems der Quelle.
func (s *Server) probeFS(ctx context.Context, dev, wantType string, n int) (fsInfo, error) {
	out, err := s.ExecOut(ctx, "blkid", "-p", "-o", "export", dev)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return fsInfo{}, cerr
		}
		return fsInfo{}, layoutErr(fmt.Sprintf("Partition %d ist nicht lesbar", n))
	}
	m := parseExport(out)
	if m["TYPE"] != wantType {
		got := m["TYPE"]
		if got == "" {
			got = "unbekannt"
		}
		return fsInfo{}, layoutErr(fmt.Sprintf("Partition %d ist %s statt %s", n, got, wantType))
	}
	fi := fsInfo{label: m["LABEL"], uuid: m["UUID"]}
	maxLabel := 16
	idOK := ext4UUIDRe.MatchString(fi.uuid)
	if wantType == "vfat" {
		maxLabel, idOK = fatLabelMax, fatIDRe.MatchString(fi.uuid)
	}
	if !idOK {
		return fsInfo{}, layoutErr(fmt.Sprintf("Partition %d hat keine lesbare UUID", n))
	}
	if fi.label != "" && (!fsLabelRe.MatchString(fi.label) || len(fi.label) > maxLabel) {
		return fsInfo{}, layoutErr(fmt.Sprintf("die Beschriftung der Partition %d wird nicht unterstützt", n))
	}
	return fi, nil
}

func (s *Server) extra() int64 {
	if s.Backup.extraBytes > 0 {
		return s.Backup.extraBytes
	}
	return filesExtraBytes
}

func (s *Server) fsUsage(path string) (int64, int64, error) {
	if s.Backup.FsUsage != nil {
		return s.Backup.FsUsage(path)
	}
	return fsUsage(path)
}

// planFiles liest die Partitionstabelle und die Dateisysteme der Quelle, misst / und berechnet das Abbild.
// Es startet nur blkid und legt nichts an.
func (s *Server) planFiles(ctx context.Context) (*filesPlan, error) {
	if err := s.checkTools(); err != nil {
		return nil, err
	}
	if s.ExecOut == nil {
		return nil, errors.New("blkid ist nicht verfügbar")
	}
	card, mustBeDevice, err := s.cardPath()
	if err != nil {
		return nil, err
	}
	if s.Backup.Source == "" {
		if err := s.checkMounts(card); err != nil {
			return nil, err
		}
	}
	dev, err := openCard(card, mustBeDevice)
	if err != nil {
		return nil, err
	}
	defer dev.Close()
	size, err := dev.Seek(0, 2)
	if err != nil {
		return nil, errors.New("Die SD-Karte ließ sich nicht öffnen: " + err.Error())
	}
	sec := make([]byte, sectorSize)
	if _, err := dev.ReadAt(sec, 0); err != nil {
		return nil, errBadPartitionTable
	}
	es, err := parseMBR(sec)
	if err != nil {
		return nil, layoutErr("die Partitionstabelle ist nicht lesbar")
	}
	if err := checkLayout(es, size/sectorSize); err != nil {
		return nil, err
	}
	p := &filesPlan{card: card, sector0: sec, oldID: binary.LittleEndian.Uint32(sec[440:]), bootSectors: int64(es[0].Sectors)}
	if p.boot, err = s.probeFS(ctx, partName(card, 1), "vfat", 1); err != nil {
		return nil, err
	}
	if p.root, err = s.probeFS(ctx, partName(card, 2), "ext4", 2); err != nil {
		return nil, err
	}
	used, inodes, err := s.fsUsage(s.Backup.rootDir())
	if err != nil {
		return nil, fmt.Errorf("Die belegte Größe des Systems ließ sich nicht ermitteln: %v", err)
	}
	p.copyBytes = used
	p.sysBytes = filesSystemSize(used, s.extra())
	p.p2Start = (filesLeadSectors + p.bootSectors + alignSectors - 1) / alignSectors * alignSectors
	p.rawSize = p.p2Start*sectorSize + p.sysBytes
	if p.sysBytes/filesInodeRatio < inodes*5/4 {
		p.inodes = inodes*5/4 + 1024
	}
	if p.rawSize/sectorSize >= 1<<32 {
		return nil, layoutErr("das Abbild wäre größer als 2 TB")
	}
	return p, nil
}

// cacheFree liefert den freien Platz für den Arbeitsordner des Downloads; fehlt er noch, zählt sein Elternordner.
func (s *Server) cacheFree() (int64, error) {
	dir := s.Backup.cacheDir()
	if _, err := os.Lstat(dir); err != nil {
		dir = filepath.Dir(dir)
	}
	return s.Backup.free(dir)
}

func (p *filesPlan) cacheNeed() int64 { return p.rawSize + cacheReserve }

func (p *filesPlan) cacheProblem(free int64) string {
	if free >= p.cacheNeed() {
		return ""
	}
	return fmt.Sprintf("Auf der SD-Karte ist nicht genug Platz für den Download (%s nötig, %s frei). Die Sicherung lässt sich auf einen USB-Stick schreiben.", fmtSize(p.cacheNeed()), fmtSize(free))
}

// filesInfo ist die Antwort von backup-info für die dateiweise Sicherung.
func (s *Server) filesInfo(ctx context.Context) helperapi.BackupInfo {
	p, err := s.planFiles(ctx)
	if err != nil {
		return helperapi.BackupInfo{Problem: err.Error(), Files: true}
	}
	info := helperapi.BackupInfo{Files: true, RawBytes: p.rawSize, NeedCard: p.cacheNeed(), NeedUSB: p.usbNeed()}
	free, ferr := s.cacheFree()
	if ferr != nil {
		info.DownloadProblem = "Der freie Platz auf der SD-Karte ist nicht lesbar: " + ferr.Error()
		return info
	}
	info.CardFree = free
	info.DownloadProblem = p.cacheProblem(free)
	return info
}

// filesMBR baut den ersten Sektor des neuen Abbilds: Startcode und Startkennzeichen der Quelle,
// neue Disk-ID, Partition 1 (Typ wie die Quelle) und 2 (Linux), sonst leer.
func filesMBR(src []byte, id uint32, p1Start, p1Sectors, p2Start, p2Sectors int64) []byte {
	out := append([]byte(nil), src[:sectorSize]...)
	clear(out[440 : sectorSize-2])
	binary.LittleEndian.PutUint32(out[440:], id)
	put := func(n int, flag, typ byte, start, sectors int64) {
		e := out[mbrTableAt+n*mbrEntrySize:]
		e[0] = flag
		e[1], e[2], e[3] = 0xfe, 0xff, 0xff
		e[4] = typ
		e[5], e[6], e[7] = 0xfe, 0xff, 0xff
		binary.LittleEndian.PutUint32(e[8:], uint32(start))
		binary.LittleEndian.PutUint32(e[12:], uint32(sectors))
	}
	put(0, src[mbrTableAt]&0x80, src[mbrTableAt+4], p1Start, p1Sectors)
	put(1, src[mbrTableAt+mbrEntrySize]&0x80, typeLinux, p2Start, p2Sectors)
	out[510], out[511] = 0x55, 0xaa
	return out
}
