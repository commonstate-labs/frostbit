//go:build windows

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"dex/utils"
	"github.com/btcsuite/btcd/btcec/v2"
	"golang.org/x/sys/windows"
)

const windowsRepoModuleLine = "module dex"

func validatePlatformSecretOptions(opts *runtimeOptions) error {
	if opts.PrivateKeyFD != -1 || opts.ArbitragePrivateKeyFD != -1 || opts.FrostWrapKeyFD != -1 || opts.KeystorePasswordFD != -1 {
		return fmt.Errorf("inherited secret FDs are unavailable on Windows; use a keystore with terminal unlock or the explicitly opted-in local debug directory")
	}
	return nil
}

func loadPlatformPrivateKey(opts *runtimeOptions) ([]byte, error) {
	if opts.PrivateKeyFD != -1 {
		return nil, fmt.Errorf("--private-key-fd is unavailable on Windows; use the explicitly opted-in local secret directory")
	}
	if opts.ArbitragePrivateKeyFD != -1 {
		return nil, fmt.Errorf("--arbitrage-private-key-fd is unavailable on Windows; use --windows-arbitrage-secret-dir")
	}
	if opts.FrostWrapKeyFD != -1 {
		return nil, fmt.Errorf("--frost-wrap-key-fd is unavailable on Windows")
	}
	secretDir := strings.TrimSpace(opts.WindowsLocalSecretDir)
	if !opts.AllowWindowsLocalSecretDir {
		if secretDir != "" {
			return nil, fmt.Errorf("--windows-local-secret-dir requires explicit --allow-windows-local-secret-dir opt-in")
		}
		return nil, fmt.Errorf("Windows local debug requires --windows-local-secret-dir and --allow-windows-local-secret-dir")
	}
	if secretDir == "" {
		return nil, fmt.Errorf("--allow-windows-local-secret-dir requires --windows-local-secret-dir")
	}
	return loadWindowsPrivateKeyFromDir(secretDir, opts.ExpectedAddress)
}

func loadPlatformArbitragePrivateKey(opts *runtimeOptions) ([]byte, error) {
	secretDir := strings.TrimSpace(opts.WindowsArbitrageSecretDir)
	if secretDir == "" {
		return nil, nil
	}
	if !opts.AllowWindowsLocalSecretDir {
		return nil, fmt.Errorf("--windows-arbitrage-secret-dir requires explicit --allow-windows-local-secret-dir opt-in")
	}
	token, err := loadWindowsPrivateKeyFromDir(secretDir, "")
	if err != nil {
		return nil, err
	}
	address, err := windowsSecretAddress(token)
	if err != nil {
		wipeBytes(token)
		return nil, err
	}
	if err := validateExpectedArbitrageAddress(opts.ExpectedArbitrageAddress, address); err != nil {
		wipeBytes(token)
		return nil, err
	}
	return token, nil
}

func loadWindowsPrivateKeyFromDir(secretDir, expectedAddress string) ([]byte, error) {
	workspaceRoot, err := findWindowsRepoRoot()
	if err != nil {
		return nil, err
	}
	secretDir, localRoot, err := resolveWindowsLocalSecretDir(workspaceRoot, secretDir)
	if err != nil {
		return nil, err
	}
	if err := rejectWindowsReparsePath(workspaceRoot, secretDir); err != nil {
		return nil, err
	}

	dirHandle, err := openWindowsSecretObject(secretDir, true)
	if err != nil {
		return nil, fmt.Errorf("open Windows local secret directory: %w", err)
	}
	defer windows.CloseHandle(dirHandle)
	if err := validateFinalWindowsPath(dirHandle, localRoot, secretDir); err != nil {
		return nil, err
	}
	if err := validateWindowsSecretHandle(dirHandle, true); err != nil {
		return nil, fmt.Errorf("validate Windows local secret directory: %w", err)
	}

	entries, err := os.ReadDir(secretDir)
	if err != nil {
		return nil, fmt.Errorf("read Windows local secret directory: %w", err)
	}
	if len(entries) != 1 {
		return nil, fmt.Errorf("Windows local secret directory must contain exactly one canonical <fbc-address>.key file; found %d entries", len(entries))
	}
	entry := entries[0]
	if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("Windows local secret entry must be a regular file")
	}
	name := entry.Name()
	if !strings.HasSuffix(name, ".key") {
		return nil, fmt.Errorf("Windows local secret filename must be canonical <fbc-address>.key")
	}
	filenameAddress := strings.TrimSuffix(name, ".key")
	if !utils.IsDexAddress(filenameAddress) || name != filenameAddress+".key" {
		return nil, fmt.Errorf("Windows local secret filename must be canonical <fbc-address>.key")
	}

	keyPath := filepath.Join(secretDir, name)
	if err := rejectWindowsReparsePath(workspaceRoot, keyPath); err != nil {
		return nil, err
	}
	beforeStat, err := os.Lstat(keyPath)
	if err != nil {
		return nil, fmt.Errorf("stat Windows local secret file: %w", err)
	}
	if !beforeStat.Mode().IsRegular() {
		return nil, fmt.Errorf("Windows local secret entry must be a regular file")
	}

	keyHandle, err := openWindowsSecretObject(keyPath, false)
	if err != nil {
		return nil, fmt.Errorf("open Windows local secret file: %w", err)
	}
	keyFile := os.NewFile(uintptr(keyHandle), keyPath)
	if keyFile == nil {
		_ = windows.CloseHandle(keyHandle)
		return nil, fmt.Errorf("open Windows local secret file: invalid handle")
	}
	defer keyFile.Close()

	openedStat, err := keyFile.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat opened Windows local secret file: %w", err)
	}
	if !openedStat.Mode().IsRegular() || !os.SameFile(beforeStat, openedStat) {
		return nil, fmt.Errorf("Windows local secret file identity changed while opening")
	}
	if err := validateFinalWindowsPath(keyHandle, localRoot, keyPath); err != nil {
		return nil, err
	}
	if err := validateWindowsSecretHandle(keyHandle, false); err != nil {
		return nil, fmt.Errorf("validate Windows local secret file: %w", err)
	}

	var beforeInfo windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(keyHandle, &beforeInfo); err != nil {
		return nil, fmt.Errorf("inspect Windows local secret file: %w", err)
	}
	if beforeInfo.NumberOfLinks != 1 {
		return nil, fmt.Errorf("Windows local secret file must not be a hard link")
	}
	raw, err := readBoundedSecret(io.Reader(keyFile), maxPrivateKeyFDBytes)
	if err != nil {
		return nil, fmt.Errorf("read Windows local secret file: %w", err)
	}
	defer wipeBytes(raw)
	var afterInfo windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(keyHandle, &afterInfo); err != nil {
		return nil, fmt.Errorf("reinspect Windows local secret file: %w", err)
	}
	if !sameWindowsFileSnapshot(beforeInfo, afterInfo) {
		return nil, fmt.Errorf("Windows local secret file changed while being read")
	}

	token, err := decodePrivateKeyToken(raw)
	if err != nil {
		return nil, fmt.Errorf("parse Windows local secret file: %w", err)
	}
	address, err := windowsSecretAddress(token)
	if err != nil {
		wipeBytes(token)
		return nil, err
	}
	if address != filenameAddress {
		wipeBytes(token)
		return nil, fmt.Errorf("Windows local secret key derives address %s, not filename address %s", address, filenameAddress)
	}
	if err := validateExpectedAddress(expectedAddress, address); err != nil {
		wipeBytes(token)
		return nil, err
	}
	return token, nil
}

                                                                          
func windowsSecretAddress(raw []byte) (string, error) {
	var scalar btcec.ModNScalar
	defer scalar.Zero()
	if len(raw) != 32 || scalar.SetByteSlice(raw) || scalar.IsZero() {
		return "", fmt.Errorf("invalid Windows private key scalar")
	}
	key, pub := btcec.PrivKeyFromBytes(raw)
	defer key.Zero()
	return utils.AddressFromSecp256k1PubKey(pub.SerializeCompressed())
}

func findWindowsRepoRoot() (string, error) {
	start, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve current working directory: %w", err)
	}
	start, err = filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve absolute current working directory: %w", err)
	}
	for candidate := filepath.Clean(start); ; candidate = filepath.Dir(candidate) {
		goMod := filepath.Join(candidate, "go.mod")
		raw, readErr := os.ReadFile(goMod)
		if readErr == nil && hasExactModuleLine(raw, windowsRepoModuleLine) {
			mainInfo, statErr := os.Stat(filepath.Join(candidate, "cmd", "main"))
			if statErr == nil && mainInfo.IsDir() {
				if err := rejectWindowsReparsePath(candidate, start); err != nil {
					return "", fmt.Errorf("validate repository working path: %w", err)
				}
				return candidate, nil
			}
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			break
		}
	}
	return "", fmt.Errorf("cannot locate repository root containing module dex and cmd/main from %s", start)
}

func hasExactModuleLine(raw []byte, expected string) bool {
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == expected {
			return true
		}
	}
	return false
}

func resolveWindowsLocalSecretDir(workspaceRoot, raw string) (string, string, error) {
	if strings.HasPrefix(raw, `\\?\`) || strings.HasPrefix(raw, `\\.\`) {
		return "", "", fmt.Errorf("Windows local secret directory must not use a device path")
	}
	secretDir, err := filepath.Abs(raw)
	if err != nil {
		return "", "", fmt.Errorf("resolve Windows local secret directory: %w", err)
	}
	secretDir = filepath.Clean(secretDir)
	volume := filepath.VolumeName(secretDir)
	if strings.HasPrefix(volume, `\\`) {
		return "", "", fmt.Errorf("Windows local secret directory must be on a local volume")
	}
	if strings.Contains(strings.TrimPrefix(secretDir, volume), ":") {
		return "", "", fmt.Errorf("Windows local secret directory must not use an alternate data stream")
	}
	localRoot := filepath.Join(workspaceRoot, ".local")
	rel, err := filepath.Rel(localRoot, secretDir)
	if err != nil || rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("Windows local secret directory must be a strict child of %s", localRoot)
	}
	return secretDir, localRoot, nil
}

func rejectWindowsReparsePath(root, target string) error {
	rel, err := filepath.Rel(root, target)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path %s escapes trusted root %s", target, root)
	}
	current := filepath.Clean(root)
	parts := []string{}
	if rel != "." {
		parts = strings.Split(rel, string(filepath.Separator))
	}
	paths := []string{current}
	for _, part := range parts {
		current = filepath.Join(current, part)
		paths = append(paths, current)
	}
	for _, path := range paths {
		ptr, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return fmt.Errorf("encode path %s: %w", path, err)
		}
		attrs, err := windows.GetFileAttributes(ptr)
		if err != nil {
			return fmt.Errorf("inspect path %s: %w", path, err)
		}
		if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("Windows local secret path contains reparse point %s", path)
		}
	}
	return nil
}

func openWindowsSecretObject(path string, directory bool) (windows.Handle, error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	flags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if directory {
		flags |= windows.FILE_FLAG_BACKUP_SEMANTICS
	}
	return windows.CreateFile(
		ptr,
		windows.GENERIC_READ|windows.READ_CONTROL,
		windows.FILE_SHARE_READ,
		nil,
		windows.OPEN_EXISTING,
		flags,
		0,
	)
}

func validateWindowsSecretHandle(handle windows.Handle, directory bool) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("object is a reparse point")
	}
	isDirectory := info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
	if isDirectory != directory {
		return fmt.Errorf("object type does not match expected directory=%t", directory)
	}

	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read security descriptor: %w", err)
	}
	control, _, err := sd.Control()
	if err != nil {
		return fmt.Errorf("read security descriptor control: %w", err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("DACL must disable inheritance")
	}

	currentUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("resolve current user SID: %w", err)
	}
	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("resolve LOCAL SYSTEM SID: %w", err)
	}
	adminSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("resolve Administrators SID: %w", err)
	}
	trusted := []*windows.SID{currentUser.User.Sid, systemSID, adminSID}
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("read owner SID: %w", err)
	}
	if !windowsSIDIn(owner, trusted) {
		return fmt.Errorf("owner SID %s is not current user, LOCAL SYSTEM, or Administrators", windowsSIDLabel(owner))
	}

	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return fmt.Errorf("object must have a non-null DACL")
	}
	currentUserCanRead := false
	for i := uint16(0); i < dacl.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(i), &ace); err != nil {
			return fmt.Errorf("read DACL ACE %d: %w", i, err)
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE:
		default:
			return fmt.Errorf("DACL ACE %d has unsupported type %d", i, ace.Header.AceType)
		}
		if ace.Mask == 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !windowsSIDIn(sid, trusted) {
			return fmt.Errorf("DACL grants access to untrusted SID %s", windowsSIDLabel(sid))
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE == 0 && sid.Equals(currentUser.User.Sid) && windowsMaskCanRead(ace.Mask) {
			currentUserCanRead = true
		}
	}
	if !currentUserCanRead {
		return fmt.Errorf("DACL must grant the current user read access")
	}
	return nil
}

func windowsSIDLabel(sid *windows.SID) string {
	if sid == nil || !sid.IsValid() {
		return "<invalid>"
	}
	return sid.String()
}

func windowsSIDIn(candidate *windows.SID, trusted []*windows.SID) bool {
	if candidate == nil || !candidate.IsValid() {
		return false
	}
	for _, sid := range trusted {
		if sid != nil && candidate.Equals(sid) {
			return true
		}
	}
	return false
}

func windowsMaskCanRead(mask windows.ACCESS_MASK) bool {
	const fileReadData windows.ACCESS_MASK = 0x0001
	return mask&(fileReadData|windows.GENERIC_READ|windows.GENERIC_ALL) != 0
}

func validateFinalWindowsPath(handle windows.Handle, localRoot, expected string) error {
	buf := make([]uint16, 512)
	for {
		n, err := windows.GetFinalPathNameByHandle(handle, &buf[0], uint32(len(buf)), 0)
		if err != nil {
			return fmt.Errorf("resolve final Windows local secret path: %w", err)
		}
		if n < uint32(len(buf)) {
			finalPath := normalizeWindowsFinalPath(windows.UTF16ToString(buf[:n]))
			rel, relErr := filepath.Rel(localRoot, finalPath)
			if relErr != nil || filepath.IsAbs(rel) || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("opened Windows local secret object resolves outside %s", localRoot)
			}
			if !strings.EqualFold(filepath.Clean(finalPath), filepath.Clean(expected)) {
				return fmt.Errorf("opened Windows local secret object resolves to an unexpected path")
			}
			return nil
		}
		buf = make([]uint16, n+1)
	}
}

func normalizeWindowsFinalPath(path string) string {
	if strings.HasPrefix(path, `\\?\UNC\`) {
		return `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
	}
	return strings.TrimPrefix(path, `\\?\`)
}

func sameWindowsFileSnapshot(a, b windows.ByHandleFileInformation) bool {
	return a.FileAttributes == b.FileAttributes &&
		a.VolumeSerialNumber == b.VolumeSerialNumber &&
		a.FileSizeHigh == b.FileSizeHigh &&
		a.FileSizeLow == b.FileSizeLow &&
		a.NumberOfLinks == b.NumberOfLinks &&
		a.FileIndexHigh == b.FileIndexHigh &&
		a.FileIndexLow == b.FileIndexLow &&
		a.LastWriteTime == b.LastWriteTime
}
