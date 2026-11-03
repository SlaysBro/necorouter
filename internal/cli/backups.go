package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// startup-config archives (available without service):
//
//	copy startup-config storage [description TEXT]
//	show backups
//	delete backup storage (NAME | *)
//
// File: /etc/necorouter/backups/startup_backup_<name>_YYYY-MM-DD_hh-mm-ss.tar.gz.
// Inside: crc (checksum of startup_backup.tar) and startup_backup.tar
// (startup-config and description). Works only with startup-config, not with running-config.
var backupNameRe = regexp.MustCompile(`^startup_backup_[A-Za-z0-9._-]{1,63}_\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}\.tar\.gz$`)

// backupCopyNode — the storage node inside copy startup-config.
func backupCopyNode() *Node {
	h := func(s *Session, v []string) error {
		desc := ""
		if len(v) > 0 {
			desc = strings.TrimSpace(v[0])
		}
		return cmdBackupCreate(s, desc)
	}
	return K("storage", "Save the startup configuration to the backup storage",
		K("description", "Add a description to the backup",
			A(KRest, "TEXT", "Description, up to 120 characters").R(h))).R(h)
}

func tarFile(tw *tar.Writer, name string, data []byte, mod time.Time) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: mod, Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

// makeBackup builds an archive from the startup-config text and the description.
func makeBackup(startup, desc string, now time.Time) ([]byte, error) {
	var inner bytes.Buffer
	tw := tar.NewWriter(&inner)
	if err := tarFile(tw, "startup-config", []byte(startup), now); err != nil {
		return nil, err
	}
	if err := tarFile(tw, "description", []byte(desc), now); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	ow := tar.NewWriter(gz)
	if err := tarFile(ow, "crc", []byte(fmt.Sprintf("%08x\n", crc32.ChecksumIEEE(inner.Bytes()))), now); err != nil {
		return nil, err
	}
	if err := tarFile(ow, "startup_backup.tar", inner.Bytes(), now); err != nil {
		return nil, err
	}
	if err := ow.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// readBackup verifies the checksum and returns startup-config and the description.
func readBackup(path string) (startup, desc string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", "", err
	}
	files := map[string][]byte{}
	read := func(r io.Reader) error {
		tr := tar.NewReader(io.LimitReader(r, 8<<20))
		for {
			h, err := tr.Next()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			b, err := io.ReadAll(tr)
			if err != nil {
				return err
			}
			files[h.Name] = b
		}
	}
	if err := read(gz); err != nil {
		return "", "", err
	}
	inner, crc := files["startup_backup.tar"], strings.TrimSpace(string(files["crc"]))
	if inner == nil || crc != fmt.Sprintf("%08x", crc32.ChecksumIEEE(inner)) {
		return "", "", fmt.Errorf("checksum mismatch")
	}
	files = map[string][]byte{}
	if err := read(bytes.NewReader(inner)); err != nil {
		return "", "", err
	}
	return string(files["startup-config"]), string(files["description"]), nil
}

func backupHostName(h string) string {
	h = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			return r
		}
		return '-'
	}, h)
	if h == "" {
		h = "necorouter"
	}
	if len(h) > 63 {
		h = h[:63]
	}
	return h
}

func cmdBackupCreate(s *Session, desc string) error {
	if err := s.needAdmin(); err != nil {
		return err
	}
	if len(desc) > 120 {
		return fmt.Errorf("description must be at most 120 characters")
	}
	for _, r := range desc {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("description must not contain control characters")
		}
	}
	b, err := os.ReadFile(s.R.P.Startup())
	if err != nil {
		return fmt.Errorf("startup-config not found: save the configuration with 'write' first")
	}
	now := time.Now()
	data, err := makeBackup(string(b), desc, now)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("startup_backup_%s_%s.tar.gz", backupHostName(s.cfg().Hostname), now.Format("2006-01-02_15-04-05"))
	path := filepath.Join(s.R.P.Backups(), name)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("backup %s already exists, try again in a second", name)
	}
	if err := writeAtomic(path, string(data)); err != nil {
		return err
	}
	s.journal("copy startup-config storage %s", name)
	s.Printf("[OK] %s\n", name)
	return nil
}

func listBackups(dir string) []string {
	ents, _ := os.ReadDir(dir)
	var n []string
	for _, e := range ents {
		if !e.IsDir() && backupNameRe.MatchString(e.Name()) {
			n = append(n, e.Name())
		}
	}
	sort.Strings(n)
	return n
}

func showBackupsNode() *Node {
	return K("backups", "Show backups of the startup configuration").R(func(s *Session, _ []string) error {
		if err := s.needAdmin(); err != nil {
			return err
		}
		names := listBackups(s.R.P.Backups())
		if len(names) == 0 {
			s.Printf("No backups (use 'copy startup-config storage').\n")
			return nil
		}
		s.Printf(" Entry  Filename%s Description\n number format YYYY-MM-DD_HH-MM-SS text\n", "")
		for i, n := range names {
			_, d, err := readBackup(filepath.Join(s.R.P.Backups(), n))
			if err != nil {
				d = "(damaged: " + err.Error() + ")"
			}
			s.Printf(" %3d.   %s %s\n", i+1, n, strings.ReplaceAll(d, " ", "_"))
		}
		return nil
	})
}

func backupDeleteNode() *Node {
	return K("backup", "Delete backups of the startup configuration",
		K("storage", "Delete from the backup storage",
			A(KWord, "NAME", "Backup file name from 'show backups', or * for all backups").R(cmdBackupDelete)))
}

func cmdBackupDelete(s *Session, v []string) error {
	if err := s.needAdmin(); err != nil {
		return err
	}
	dir := s.R.P.Backups()
	var del []string
	if v[0] == "*" {
		del = listBackups(dir)
		if len(del) == 0 {
			return fmt.Errorf("no backups")
		}
	} else {
		if !backupNameRe.MatchString(v[0]) {
			return fmt.Errorf("invalid backup name %q (see 'show backups')", v[0])
		}
		if _, err := os.Stat(filepath.Join(dir, v[0])); err != nil {
			return fmt.Errorf("backup %q not found", v[0])
		}
		del = []string{v[0]}
	}
	return s.ask(fmt.Sprintf("%% %d backup(s) will be deleted.\n", len(del)), "Continue?", func(s *Session) error {
		for _, n := range del {
			if err := os.Remove(filepath.Join(dir, n)); err != nil {
				return err
			}
			s.journal("delete backup storage %s", n)
		}
		s.Printf("[OK] deleted: %d\n", len(del))
		return nil
	})
}
