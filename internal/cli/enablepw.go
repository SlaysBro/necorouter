package cli

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// Enable mode password (enable password / enable secret). Only a hash is stored in the configuration:
// "salt$PBKDF2-SHA256" (a string of 8 hex salt characters and 64 hex hash characters); the plaintext password
// is not stored anywhere. DES hash is not supported: the enable password 8 <hash> format is its own.
var (
	enableHashRe = regexp.MustCompile(`^[0-9a-f]{8}\$[0-9a-f]{64}$`)
	enablePassRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{0,7}$`)
)

const enableIter = 50000

func enableHash(pass, salt string) (string, error) {
	k, err := pbkdf2.Key(sha256.New, pass, []byte(salt), enableIter, 32)
	if err != nil {
		return "", err
	}
	return salt + "$" + hex.EncodeToString(k), nil
}

func newEnableHash(pass string) (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return enableHash(pass, hex.EncodeToString(b[:]))
}

func checkEnablePass(hash, pass string) bool {
	salt, _, ok := strings.Cut(hash, "$")
	if !ok {
		return false
	}
	h, err := enableHash(pass, salt)
	return err == nil && subtle.ConstantTimeCompare([]byte(h), []byte(hash)) == 1
}

// askSecret asks a question with input without echo: the next line is the answer, passed to do.
// Not used while replaying a configuration.
func (s *Session) askSecret(question string, do func(*Session, string) error) {
	s.pending = &confirmation{question: question, secret: true, doIn: do}
}

// PromptSecret tells the client that input must be hidden (echo off).
func (s *Session) PromptSecret() bool { return s.pending != nil && s.pending.secret }

func setEnablePass(s *Session, pass string) error {
	if !enablePassRe.MatchString(pass) {
		return fmt.Errorf("password must be 1-8 latin letters or digits and start with a letter")
	}
	h, err := newEnableHash(pass)
	if err != nil {
		return err
	}
	s.cfg().EnablePass = h
	return nil
}

func enablePassNodes() []*Node {
	word := A(KWord, "PASSWORD", "Password: 1-8 latin letters or digits, starts with a letter (stored as a hash)").R(
		func(s *Session, v []string) error { return setEnablePass(s, v[0]) })
	hashed := K("8", "A hashed password follows (as shown by 'show running-config')",
		A(KWord, "HASH", "Hash of the password").Chk(enableHashRe.MatchString).R(func(s *Session, v []string) error {
			s.cfg().EnablePass = v[0]
			return nil
		}))
	clear := func(s *Session, _ []string) error { s.cfg().EnablePass = ""; return nil }
	pw := K("password", "Set the password for privileged EXEC mode", hashed, word).N(clear).Sec()
	sec := K("secret", "Set the password for privileged EXEC mode", K("8", "A hashed password follows", hashed.Kids...), word).N(clear).Sec()
	return []*Node{K("enable", "Set the password for privileged EXEC mode", pw, sec)}
}
