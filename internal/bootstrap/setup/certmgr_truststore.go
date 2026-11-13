// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"crypto/x509"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/pavlo-v-chernykh/keystore-go/v4"

	"github.com/zextras/carbonio-configd/internal/pkcs12"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// What keytool says when it cannot open a store, and about an alias: the
// text of the error is what the script showed, the program is not run any
// more.
const (
	// certKeytoolTampered is for a JKS keystore.
	certKeytoolTampered = "keytool error: java.io.IOException: Keystore was tampered with, or password was incorrect"
	// certKeytoolBadPass is for a PKCS12 keystore.
	certKeytoolBadPass = "keytool error: java.io.IOException: keystore password was incorrect"
	// certKeytoolFormat is for a file that is no keystore at all.
	certKeytoolFormat = "keytool error: java.security.KeyStoreException: " +
		"Unrecognized keystore format. Please load it with a specified type"
	// certKeytoolNoCert is for a file that is no certificate.
	certKeytoolNoCert = "keytool error: java.lang.Exception: Input not an X.509 certificate"
	// certKeytoolIO starts the message of an error of the file system.
	certKeytoolIO = "keytool error: java.io.IOException: "
)

// jksMagic is the start of a JKS file (0xFEEDFEED).
var jksMagic = []byte{0xFE, 0xED, 0xFE, 0xED}

// trustStore is a Java keystore of the kinds keytool reads: JKS and PKCS12.
// The aliases are not case sensitive.
type trustStore interface {
	// get is the DER of the certificate of an alias (the first one of a
	// private key entry).
	get(alias string) ([]byte, bool)
	// set puts a certificate under an alias, as a trusted one.
	set(alias string, cert *x509.Certificate)
	// remove deletes an alias; one that is not there is not an error.
	remove(alias string)
	// encode is the file for the password.
	encode(pass string) ([]byte, error)
}

// p12Store is a PKCS12 keystore.
type p12Store struct{ s *pkcs12.Store }

func (p p12Store) get(alias string) ([]byte, bool) {
	e, ok := p.s.Get(alias)

	return e.Cert, ok
}

func (p p12Store) set(alias string, cert *x509.Certificate) { p.s.Set(alias, cert.Raw) }
func (p p12Store) remove(alias string)                      { p.s.Delete(alias) }
func (p p12Store) encode(pass string) ([]byte, error)       { return p.s.Encode(pass) }

// jksStore is a JKS keystore.
type jksStore struct {
	ks  keystore.KeyStore
	now time.Time
}

func (j jksStore) get(alias string) ([]byte, bool) {
	switch {
	case j.ks.IsPrivateKeyEntry(alias):
		chain, err := j.ks.GetPrivateKeyEntryCertificateChain(alias)
		if err == nil && len(chain) > 0 {
			return chain[0].Content, true
		}
	case j.ks.IsTrustedCertificateEntry(alias):
		entry, err := j.ks.GetTrustedCertificateEntry(alias)
		if err == nil {
			return entry.Certificate.Content, true
		}
	}

	return nil, false
}

func (j jksStore) set(alias string, cert *x509.Certificate) {
	// The entry is well formed: it cannot fail.
	_ = j.ks.SetTrustedCertificateEntry(alias, keystore.TrustedCertificateEntry{
		CreationTime: j.now,
		Certificate:  keystore.Certificate{Type: certX509, Content: cert.Raw},
	})
}

func (j jksStore) remove(alias string) { j.ks.DeleteEntry(alias) }

func (j jksStore) encode(pass string) ([]byte, error) {
	var buf bytes.Buffer

	if err := j.ks.Store(&buf, []byte(pass)); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// openTrustStore reads a keystore, as keytool does: JKS or PKCS12, whatever
// the file is. A file that is not there is an empty PKCS12 store when create
// is set (the type keytool makes) and an error if not. The errors have the
// text keytool gives.
func (m *certMgr) openTrustStore(file, pass string, create bool) (trustStore, error) {
	data, err := m.slurp(file)

	switch {
	case errors.Is(err, os.ErrNotExist) && create:
		return p12Store{pkcs12.New()}, nil
	case errors.Is(err, os.ErrNotExist):
		return nil, errors.New("keytool error: java.io.FileNotFoundException: " + file + " (No such file or directory)")
	case err != nil:
		return nil, errors.New(certKeytoolIO + certErrMessage(err))
	case bytes.HasPrefix(data, jksMagic):
		return m.openJKSStore(data, pass)
	}

	s, err := pkcs12.Decode(data, pass)

	switch {
	case err == nil:
		return p12Store{s}, nil
	case errors.Is(err, pkcs12.ErrPassword):
		return nil, errors.New(certKeytoolBadPass)
	case errors.Is(err, pkcs12.ErrFormat):
		return nil, errors.New(certKeytoolFormat)
	}

	return nil, errors.New(certKeytoolIO + err.Error())
}

func (m *certMgr) openJKSStore(data []byte, pass string) (trustStore, error) {
	ks := keystore.New(keystore.WithOrderedAliases())
	if err := ks.Load(bytes.NewReader(data), []byte(pass)); err != nil {
		return nil, errors.New(certKeytoolTampered)
	}

	return jksStore{ks: ks, now: m.e.Sys.Clock.Now()}, nil
}

// saveTrustStore writes a keystore over its file.
func (m *certMgr) saveTrustStore(ts trustStore, file, pass string) error {
	data, err := ts.encode(pass)
	if err != nil {
		return err
	}

	return m.replaceFile(file, data, certModeFile)
}

// certFromFile is the first certificate of a file keytool -import reads: PEM
// (the first block that is a certificate) or DER.
func certFromFile(data []byte) (*x509.Certificate, error) {
	if certs, err := certParseCerts(data); err == nil && len(certs) > 0 {
		return certs[0], nil
	}

	cert, err := x509.ParseCertificate(data)
	if err != nil {
		return nil, errors.New(certKeytoolNoCert)
	}

	return cert, nil
}

// fileOwner is the owner of a file, when the system tells it.
func fileOwner(fi fs.FileInfo) (uid, gid int, ok bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}

	return int(st.Uid), int(st.Gid), true
}

// fileDest is the mode and the owner of a file that replaces another.
type fileDest struct {
	mode     fs.FileMode
	uid, gid int
	chown    bool
}

// replaceFile writes a file the way a reader never sees it half written: the
// data goes to a new file in the directory, which is renamed over it. The
// file keeps its mode and its owner; a new one gets perm and, when the
// process is root, zextras as its owner (as writeFile does). A symbolic link
// stays one: the file it leads to is replaced, in its own directory (on a
// node the cacerts of the JDK is a link into etc/). Root follows the links
// of the directories only root can change, as resolve says. zextras, which
// cannot create files in a directory of root, writes in place then.
func (m *certMgr) replaceFile(name string, data []byte, perm fs.FileMode) error {
	name, err := m.resolve(name)
	if err != nil {
		return err
	}

	fi, err := m.e.Sys.FS.Lstat(name)

	dest, err := m.destination(fi, err == nil, perm)
	if err != nil {
		return err
	}

	f, err := m.e.Sys.FS.CreateTemp(filepath.Dir(name), "."+filepath.Base(name)+".*")
	if errors.Is(err, fs.ErrPermission) && m.e.IsZextras {
		return m.writeFile(name, data, perm)
	}

	if err != nil {
		return err
	}

	if err = fillTemp(f, data, dest); err == nil {
		err = m.e.Sys.FS.Rename(f.Name(), name)
	}

	if err != nil {
		_ = m.e.Sys.FS.Remove(f.Name())
	}

	return err
}

// fillTemp gives the new file its mode and owner, writes the data and closes it.
func fillTemp(f sys.File, data []byte, dest fileDest) error {
	err := f.Chmod(dest.mode)
	if err == nil && dest.chown {
		err = f.Chown(dest.uid, dest.gid)
	}

	if err == nil {
		_, err = f.Write(data)
	}

	if s, ok := f.(interface{ Sync() error }); ok && err == nil {
		err = s.Sync()
	}

	if cerr := f.Close(); err == nil {
		err = cerr
	}

	return err
}

// destination is the mode and owner the file that replaces another has: the
// ones of the file (fi, when exists), or perm and zextras for a new one.
// Only root changes the owner.
func (m *certMgr) destination(fi fs.FileInfo, exists bool, perm fs.FileMode) (fileDest, error) {
	if exists {
		uid, gid, known := fileOwner(fi)

		return fileDest{fi.Mode().Perm(), uid, gid, known && !m.e.IsZextras}, nil
	}

	if m.e.IsZextras {
		return fileDest{mode: perm}, nil
	}

	if err := m.loadIDs(); err != nil {
		return fileDest{}, err
	}

	return fileDest{perm, m.zuid, m.zgid, true}, nil
}
