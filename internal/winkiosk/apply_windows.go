//go:build windows

package winkiosk

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// ACHTUNG: Dieser Code verändert das System (Benutzer, Profil, Registry, LSA, Rechte). Er darf nur auf dem
// Zielgerät laufen, nie auf dem Entwicklungsrechner; getestet wird die Logik in plan.go mit einem Fake.

var (
	procLsaQueryInfoPolicy = advapi32.NewProc("LsaQueryInformationPolicy")
	procLsaFreeMemory      = advapi32.NewProc("LsaFreeMemory")

	wtsapi32                = windows.NewLazySystemDLL("wtsapi32.dll")
	procWTSFreeMemory       = wtsapi32.NewProc("WTSFreeMemory")
	procWTSQuerySessionInfo = wtsapi32.NewProc("WTSQuerySessionInformationW")
	procWTSLogoffSession    = wtsapi32.NewProc("WTSLogoffSession")

	netapi32 = windows.NewLazySystemDLL("netapi32.dll")
	userenv  = windows.NewLazySystemDLL("userenv.dll")
	advapi32 = windows.NewLazySystemDLL("advapi32.dll")

	procNetUserAdd       = netapi32.NewProc("NetUserAdd")
	procNetUserDel       = netapi32.NewProc("NetUserDel")
	procCreateProfile    = userenv.NewProc("CreateProfile")
	procDeleteProfile    = userenv.NewProc("DeleteProfileW")
	procRegLoadKey       = advapi32.NewProc("RegLoadKeyW")
	procRegUnLoadKey     = advapi32.NewProc("RegUnLoadKeyW")
	procLsaOpenPolicy    = advapi32.NewProc("LsaOpenPolicy")
	procLsaClose         = advapi32.NewProc("LsaClose")
	procLsaStorePrivate  = advapi32.NewProc("LsaStorePrivateData")
	procLsaNtStatusToWin = advapi32.NewProc("LsaNtStatusToWinError")
)

const (
	userPrivUser            = 1
	ufScript                = 0x0001
	ufPasswdCantChange      = 0x0040
	ufDontExpirePasswd      = 0x10000
	nerrUserNotFound        = 2221
	policyCreateSecret      = 0x00000020
	statusNameNotFound      = 0xC0000034
	errPathNotFound         = 3
	errFileNotFound         = 2
	errAlreadyExists        = 0x800700B7 // HRESULT_FROM_WIN32(ERROR_ALREADY_EXISTS)
	errNoneMapped           = 1332
	wtsUserName             = 5
	policyViewLocalInfo     = 0x1
	policyAccountDomainInfo = 5
	wtsDomainName           = 7
	accessFullControl       = 0x1F01FF // FILE_ALL_ACCESS
	accessModify            = 0x1301BF // Ändern: lesen, schreiben, ausführen, löschen
	profileListKey          = `SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList\`
	editionKey              = `SOFTWARE\Microsoft\Windows NT\CurrentVersion`
)

type userInfo1 struct {
	Name        *uint16
	Password    *uint16
	PasswordAge uint32
	Priv        uint32
	HomeDir     *uint16
	Comment     *uint16
	Flags       uint32
	ScriptPath  *uint16
}

type lsaObjectAttributes struct {
	Length                   uint32
	RootDirectory            uintptr
	ObjectName               *windows.NTUnicodeString
	Attributes               uint32
	SecurityDescriptor       uintptr
	SecurityQualityOfService uintptr
}

// Edition liest die Windows-Edition (EditionID), z. B. "Professional" oder "Core".
func Edition() (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, editionKey, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "", err
	}
	defer k.Close()
	v, _, err := k.GetStringValue("EditionID")
	return v, err
}

// DataDir ist %ProgramData%\PDH-Terminal.
func dataDir() string {
	pd := os.Getenv("ProgramData")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	return filepath.Join(pd, "PDH-Terminal")
}

// LogPath ist die Protokolldatei des Helfers.
func LogPath() string { return filepath.Join(dataDir(), "log", "kiosk.log") }

// System setzt Exec mit Win32 um. Es schreibt nie das Passwort ins Protokoll.
type System struct {
	log io.Writer
}

// NewSystem liefert die Win32-Umsetzung; Protokollzeilen gehen nach log (darf nil sein).
func NewSystem(log io.Writer) *System { return &System{log: log} }

var _ Exec = (*System)(nil)

func (s *System) Logf(format string, args ...any) {
	if s.log != nil {
		fmt.Fprintf(s.log, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
	}
}

func (s *System) DataDir() string { return dataDir() }

func errno(r uintptr) error { return syscall.Errno(r) }

// utf16Secret wandelt ASCII-Bytes in UTF-16 mit Endnull; wipe überschreibt die Kopie.
func utf16Secret(b []byte) (u []uint16, wipe func()) {
	u = make([]uint16, len(b)+1)
	for i, c := range b {
		u[i] = uint16(c)
	}
	return u, func() { clear(u) }
}

func (s *System) CreateUser(name string, password []byte) error {
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	pw, wipe := utf16Secret(password)
	defer wipe()
	ui := userInfo1{Name: n, Password: &pw[0], Priv: userPrivUser,
		Flags: ufScript | ufPasswdCantChange | ufDontExpirePasswd}
	var parmErr uint32
	r, _, _ := procNetUserAdd.Call(0, 1, uintptr(unsafe.Pointer(&ui)), uintptr(unsafe.Pointer(&parmErr)))
	if r != 0 {
		return fmt.Errorf("NetUserAdd: %w", errno(r))
	}
	return nil
}

func (s *System) DeleteUser(name string) error {
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	r, _, _ := procNetUserDel.Call(0, uintptr(unsafe.Pointer(n)))
	switch r {
	case 0:
		return nil
	case nerrUserNotFound:
		return ErrNotFound
	}
	return fmt.Errorf("NetUserDel: %w", errno(r))
}

// userSID sucht die SID des lokalen Benutzers; ErrNotFound, wenn es ihn nicht gibt.
func userSID(user string) (*windows.SID, error) {
	sid, _, _, err := windows.LookupSID("", user)
	if err != nil {
		if errors.Is(err, syscall.Errno(errNoneMapped)) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("Benutzer %s: %w", user, err)
	}
	return sid, nil
}

func (s *System) CreateProfile(user string) (string, error) {
	sid, err := userSID(user)
	if err != nil {
		return "", err
	}
	sidStr, err := windows.UTF16PtrFromString(sid.String())
	if err != nil {
		return "", err
	}
	name, _ := windows.UTF16PtrFromString(user)
	buf := make([]uint16, windows.MAX_PATH+1)
	r, _, _ := procCreateProfile.Call(uintptr(unsafe.Pointer(sidStr)), uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if r == errAlreadyExists {
		return profileImagePath(sid.String())
	}
	if r != 0 { // HRESULT
		return "", fmt.Errorf("CreateProfile: HRESULT 0x%08X", uint32(r))
	}
	return windows.UTF16ToString(buf), nil
}

// profileImagePath liest ProfileImagePath des Profils mit der SID sid; ErrNotFound, wenn es ihn nicht gibt.
func profileImagePath(sid string) (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, profileListKey+sid, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return "", ErrNotFound
		}
		return "", err
	}
	defer k.Close()
	v, _, err := k.GetStringValue("ProfileImagePath")
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return "", ErrNotFound
		}
		return "", err
	}
	return registry.ExpandString(v)
}

// localDomainSID ist die SID der lokalen Kontodomäne dieses Rechners (S-1-5-21-a-b-c ohne RID), über
// LsaQueryInformationPolicy(PolicyAccountDomainInformation).
func localDomainSID() (string, error) {
	h, err := lsaOpenAccess(policyViewLocalInfo)
	if err != nil {
		return "", err
	}
	defer procLsaClose.Call(h)
	type domainInfo struct {
		DomainName windows.NTUnicodeString
		DomainSid  *windows.SID
	}
	var info *domainInfo // LSA legt hier den Zeiger auf seinen Puffer ab
	r, _, _ := procLsaQueryInfoPolicy.Call(h, policyAccountDomainInfo, uintptr(unsafe.Pointer(&info)))
	if r != 0 {
		return "", lsaErr("LsaQueryInformationPolicy", r)
	}
	defer procLsaFreeMemory.Call(uintptr(unsafe.Pointer(info)))
		return "", errors.New("keine lokale Domänen-SID")
	}
	return info.DomainSid.String(), nil
}

// OwnsProfileSID: siehe Exec. Unauflösbar zählt nur mit dem SID-Präfix der lokalen Kontodomäne als Waise, jeder
// andere Fehler (z. B. Domänencontroller nicht erreichbar) als "nicht unseres".
func (s *System) OwnsProfileSID(sid, user string) (bool, error) {
	p, err := windows.StringToSid(sid)
	if err != nil {
		return false, err
	}
	account, domain, _, err := p.LookupAccount("")
	res := Resolved
	if err != nil {
		if errors.Is(err, syscall.Errno(errNoneMapped)) {
			res = Unmapped
		} else {
			res = LookupFailed
		}
	}
	host, err := windows.ComputerName()
	if err != nil {
		return false, fmt.Errorf("Computername: %w", err)
	}
	prefix := ""
	if res == Unmapped {
		if prefix, err = localDomainSID(); err != nil {
			return false, err
		}
	}
	return OwnProfileSID(sid, res, account, domain, user, host, prefix), nil
}

func (s *System) UserSID(user string) (string, error) {
	sid, err := userSID(user)
	if err != nil {
		return "", err
	}
	return sid.String(), nil
}

func (s *System) ProfilePathBySID(sid string) (string, error) { return profileImagePath(sid) }

func (s *System) DeleteProfileBySID(sid string) error {
	if _, err := profileImagePath(sid); err != nil {
		return err
	}
	p, err := windows.UTF16PtrFromString(sid)
	if err != nil {
		return err
	}
	r, _, e := procDeleteProfile.Call(uintptr(unsafe.Pointer(p)), 0, 0)
	if r == 0 {
		if errors.Is(e, syscall.Errno(errFileNotFound)) || errors.Is(e, syscall.Errno(errPathNotFound)) {
			return ErrNotFound
		}
		return fmt.Errorf("DeleteProfile: %w", e)
	}
	return nil
}

// FindOrphanProfiles durchsucht die ProfileList nach Ordnern PDH-Kiosk oder PDH-Kiosk.*, behält aber nur SIDs,
// die zum lokalen Konto gehören oder unauflösbar sind.
func (s *System) FindOrphanProfiles(user string) ([]string, error) {
	pl, err := registry.OpenKey(registry.LOCAL_MACHINE, strings.TrimSuffix(profileListKey, `\`), registry.ENUMERATE_SUB_KEYS|registry.WOW64_64KEY)
	if err != nil {
		return nil, nil
	}
	defer pl.Close()
	names, _ := pl.ReadSubKeyNames(-1)
	var out []string
	for _, n := range names {
		if !strings.HasPrefix(n, "S-1-5-21-") {
			continue
		}
		p, err := profileImagePath(n)
		if err != nil {
			continue
		}
		base := strings.ToLower(filepath.Base(strings.ReplaceAll(p, `/`, `\`)))
		if base == strings.ToLower(user) || strings.HasPrefix(base, strings.ToLower(user)+".") {
			if own, err := s.OwnsProfileSID(n, user); err == nil && own {
				out = append(out, n)
			}
		}
	}
	return out, nil
}

// sessionsOf sind die Sitzungen (ID), in denen das lokale Konto user angemeldet ist; ein Domänenkonto gleichen
// Namens zählt nicht.
func sessionsOf(user string) ([]uint32, error) {
	host, err := windows.ComputerName()
	if err != nil {
		return nil, fmt.Errorf("Computername: %w", err)
	}
	var info *windows.WTS_SESSION_INFO
	var n uint32
	if err := windows.WTSEnumerateSessions(0, 0, 1, &info, &n); err != nil {
		return nil, fmt.Errorf("WTSEnumerateSessions: %w", err)
	}
	defer procWTSFreeMemory.Call(uintptr(unsafe.Pointer(info)))
	query := func(id uint32, class uintptr) string {
		var buf *uint16
		var size uint32
		r, _, _ := procWTSQuerySessionInfo.Call(0, uintptr(id), class, uintptr(unsafe.Pointer(&buf)), uintptr(unsafe.Pointer(&size)))
		if r == 0 {
			return ""
		}
		defer procWTSFreeMemory.Call(uintptr(unsafe.Pointer(buf)))
		return windows.UTF16PtrToString(buf)
	}
	var out []uint32
	for _, si := range unsafe.Slice(info, n) {
		if LocalAccount(query(si.SessionID, wtsUserName), query(si.SessionID, wtsDomainName), user, host) {
			out = append(out, si.SessionID)
		}
	}
	return out, nil
}

func (s *System) LogoffUser(user string) error {
	ids, err := sessionsOf(user)
	if err != nil {
		return err
	}
	for _, id := range ids {
		procWTSLogoffSession.Call(0, uintptr(id), 1) // wartet auf das Ende
	}
	for try := 0; try < 20 && len(ids) > 0; try++ {
		if ids, err = sessionsOf(user); err != nil || len(ids) == 0 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err == nil && len(ids) > 0 {
		return fmt.Errorf("Sitzung von %s besteht noch", user)
	}
	return err
}

// enablePrivileges schaltet SE_RESTORE und SE_BACKUP ein, die RegLoadKey und RegUnLoadKey brauchen.
func enablePrivileges() error {
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &tok); err != nil {
		return fmt.Errorf("Prozess-Token: %w", err)
	}
	defer tok.Close()
	for _, name := range []string{"SeRestorePrivilege", "SeBackupPrivilege"} {
		n, _ := windows.UTF16PtrFromString(name)
		var luid windows.LUID
		if err := windows.LookupPrivilegeValue(nil, n, &luid); err != nil {
			return fmt.Errorf("Recht %s: %w", name, err)
		}
		tp := windows.Tokenprivileges{PrivilegeCount: 1}
		tp.Privileges[0] = windows.LUIDAndAttributes{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}
		if err := windows.AdjustTokenPrivileges(tok, false, &tp, 0, nil, nil); err != nil {
			return fmt.Errorf("Recht %s: %w", name, err)
		}
	}
	return nil
}

func hiveLoaded(key string) bool {
	k, err := registry.OpenKey(registry.USERS, key, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	k.Close()
	return true
}

func (s *System) LoadHive(profilePath, key string) error {
	if hiveLoaded(key) {
		return nil
	}
	if err := enablePrivileges(); err != nil {
		return err
	}
	file := filepath.Join(profilePath, "NTUSER.DAT")
	k, err := windows.UTF16PtrFromString(key)
	if err != nil {
		return err
	}
	f, err := windows.UTF16PtrFromString(file)
	if err != nil {
		return err
	}
	r, _, _ := procRegLoadKey.Call(uintptr(registry.USERS), uintptr(unsafe.Pointer(k)), uintptr(unsafe.Pointer(f)))
	if r != 0 {
		return fmt.Errorf("RegLoadKey: %w", errno(r))
	}
	return nil
}

func (s *System) UnloadHive(key string) error {
	if !hiveLoaded(key) {
		return ErrNotFound
	}
	if err := enablePrivileges(); err != nil {
		return err
	}
	k, err := windows.UTF16PtrFromString(key)
	if err != nil {
		return err
	}
	var r uintptr
	for try := 0; try < 10; try++ { // Handles werden mitunter erst verzögert frei
		r, _, _ = procRegUnLoadKey.Call(uintptr(registry.USERS), uintptr(unsafe.Pointer(k)))
		if r == 0 {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	if r == errFileNotFound || r == errPathNotFound {
		return ErrNotFound
	}
	return fmt.Errorf("RegUnLoadKey: %w", errno(r))
}

// regKey öffnet (oder legt an) den Schlüssel unter HKLM oder im geladenen Kiosk-Hive.
func regKey(root, path string, create bool, access uint32) (registry.Key, error) {
	base, sub := registry.LOCAL_MACHINE, path
	switch root {
	case HKLM:
	case Kiosk:
		base, sub = registry.USERS, HiveKey+`\`+path
	default:
		return 0, fmt.Errorf("unbekannte Wurzel %q", root)
	}
	access |= registry.WOW64_64KEY
	var k registry.Key
	var err error
	if create {
		k, _, err = registry.CreateKey(base, sub, access)
	} else {
		k, err = registry.OpenKey(base, sub, access)
	}
	if errors.Is(err, registry.ErrNotExist) {
		return 0, ErrNotFound
	}
	return k, err
}

func (s *System) SetRegString(root, path, name, value string) error {
	k, err := regKey(root, path, true, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(name, value)
}

func (s *System) SetRegDWORD(root, path, name string, value uint32) error {
	k, err := regKey(root, path, true, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetDWordValue(name, value)
}

func (s *System) GetRegString(root, path, name string) (string, error) {
	k, err := regKey(root, path, false, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return "", ErrNotFound
	}
	return v, err
}

func (s *System) DeleteRegValue(root, path, name string) error {
	k, err := regKey(root, path, false, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(name); err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func lsaOpen() (uintptr, error) { return lsaOpenAccess(policyCreateSecret) }

func lsaOpenAccess(access uintptr) (uintptr, error) {
	oa := lsaObjectAttributes{Length: uint32(unsafe.Sizeof(lsaObjectAttributes{}))}
	var h uintptr
	r, _, _ := procLsaOpenPolicy.Call(0, uintptr(unsafe.Pointer(&oa)), access, uintptr(unsafe.Pointer(&h)))
	if r != 0 {
		return 0, lsaErr("LsaOpenPolicy", r)
	}
	return h, nil
}

func lsaErr(what string, status uintptr) error {
	if status == statusNameNotFound {
		return ErrNotFound
	}
	w, _, _ := procLsaNtStatusToWin.Call(status)
	return fmt.Errorf("%s: %w", what, errno(w))
}

// StoreLSASecret legt value als LSA-Geheimnis name ab (nil/leer löscht es).
func (s *System) StoreLSASecret(name string, value []byte) error {
	h, err := lsaOpen()
	if err != nil {
		return err
	}
	defer procLsaClose.Call(h)
	key, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return err
	}
	pw, wipe := utf16Secret(value)
	defer wipe()
	data := windows.NTUnicodeString{Length: uint16(len(value) * 2), MaximumLength: uint16(len(pw) * 2), Buffer: &pw[0]}
	r, _, _ := procLsaStorePrivate.Call(h, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&data)))
	if r != 0 {
		return lsaErr("LsaStorePrivateData", r)
	}
	return nil
}

func (s *System) DeleteLSASecret(name string) error {
	h, err := lsaOpen()
	if err != nil {
		return err
	}
	defer procLsaClose.Call(h)
	key, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return err
	}
	r, _, _ := procLsaStorePrivate.Call(h, uintptr(unsafe.Pointer(key)), 0) // nil löscht
	if r != 0 {
		return lsaErr("LsaStorePrivateData", r)
	}
	return nil
}

func access(sid *windows.SID, mask uint32) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.ACCESS_MASK(mask),
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}

func wellKnown(t windows.WELL_KNOWN_SID_TYPE) (*windows.SID, error) {
	return windows.CreateWellKnownSid(t)
}

// SetDataACL: nur Administratoren und SYSTEM (Vollzugriff) und der Kiosk-Benutzer (Ändern), vererbt, ohne
// Rechte aus dem übergeordneten Ordner.
func (s *System) SetDataACL(dir, user string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	admins, err := wellKnown(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}
	system, err := wellKnown(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	u, err := userSID(user)
	if err != nil {
		return err
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{
		access(admins, accessFullControl), access(system, accessFullControl), access(u, accessModify),
	}, nil)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

// RestoreDataACL stellt den Zustand der Zusatz-Einrichtung her: Vererbung vom übergeordneten Ordner wieder
// an, dazu "Benutzer: Ändern" (wie im Setup users-modify); der Kiosk-Eintrag entfällt.
func (s *System) RestoreDataACL(dir string) error {
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	users, err := wellKnown(windows.WinBuiltinUsersSid)
	if err != nil {
		return err
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{access(users, accessModify)}, nil)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

func (s *System) GetRegDWORD(root, path, name string) (uint32, error) {
	k, err := regKey(root, path, false, registry.QUERY_VALUE)
	if err != nil {
		return 0, err
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return 0, ErrNotFound
	}
	return uint32(v), err
}

func (s *System) DeleteRegKey(root, path string) error {
	base, sub := registry.LOCAL_MACHINE, path
	if root == Kiosk {
		base, sub = registry.USERS, HiveKey+`\`+path
	}
	if err := registry.DeleteKey(base, sub); err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	return nil
}
