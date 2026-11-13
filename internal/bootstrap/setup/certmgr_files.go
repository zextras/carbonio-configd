// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"github.com/zextras/carbonio-configd/internal/sys"
)

// The modes of the files zmcertmgr creates: the script runs with umask 027
// (and 022 while it installs the CA certificate); openssl creates the keys it
// generates 0600.
const (
	certModeFile   = 0o640
	certModeKey    = 0o600
	certModePublic = 0o644
	certModeDir    = 0o750
	certModeCADir  = 0o755
)

// certTyped are the files of a certificate type.
type certTyped struct {
	crt, csr, key string
	// ca is the CA file the certificate is verified against (the commercial
	// chain, or the self-signed CA).
	ca string
}

// certPaths are the files and directories of sslDirs and sslFiles of the
// script.
type certPaths struct {
	confDir, confCA          string
	ssl, caDir, commDir      string
	selfDir, newcerts        string
	caCrt, caKey, caSrl      string
	index, confInput         string
	confCert, confCAFile     string
	confCACrt, confCAKey     string
	commCACrt                string
	mailboxdPEM, mailboxdDER string
	cacerts, keystore        string
	self, comm               certTyped
}

// newCertPaths lays out the files under the Carbonio home of m.
func newCertPaths(m *certMgr) certPaths {
	path := m.e.Paths.Path
	p := certPaths{
		confDir: path(certDirConf), confCA: path(certDirConf, "ca"),
		ssl: path(certDirSSL, certDirCarbonio), caDir: path(certDirSSL, certDirCarbonio, "ca"),
		commDir: path(certDirSSL, certDirCarbonio, "commercial"), selfDir: path(certDirSSL, certDirCarbonio, "server"),
		newcerts: path(certDirSSL, certDirCarbonio, "ca", "newcerts"),
		caCrt:    path(certDirSSL, certDirCarbonio, "ca", "ca.pem"), caKey: path(certDirSSL, certDirCarbonio, "ca", "ca.key"),
		caSrl:      path(certDirSSL, certDirCarbonio, "ca", "ca.srl"),
		index:      path(certDirSSL, certDirCarbonio, "ca", "index.txt"),
		confCAFile: path(certDirSSL, certDirCarbonio, "ca", "zmssl.cnf"),
		confInput:  path(certDirConf, "zmssl.cnf.in"), confCert: path(certDirConf, "zmssl.cnf"),
		confCACrt: path(certDirConf, "ca", "ca.pem"), confCAKey: path(certDirConf, "ca", "ca.key"),
		commCACrt:   path(certDirSSL, certDirCarbonio, "commercial", "commercial_ca.crt"),
		mailboxdPEM: path(certSvcMailboxd, "etc", "mailboxd.pem"), mailboxdDER: path(certSvcMailboxd, "etc", "mailboxd.der"),
		cacerts: m.lc["mailboxd_truststore"], keystore: m.lc["mailboxd_keystore"],
	}

	p.self = certTyped{
		crt: filepath.Join(p.selfDir, "server.crt"), csr: filepath.Join(p.selfDir, "server.csr"),
		key: filepath.Join(p.selfDir, "server.key"), ca: p.caCrt,
	}
	p.comm = certTyped{
		crt: filepath.Join(p.commDir, "commercial.crt"), csr: filepath.Join(p.commDir, "commercial.csr"),
		key: filepath.Join(p.commDir, "commercial.key"), ca: p.commCACrt,
	}

	return p
}

// typed returns the files of "self" or "comm".
func (p *certPaths) typed(typ string) certTyped {
	if typ == certTypeComm {
		return p.comm
	}

	return p.self
}

// svcCrt is the certificate file of a service (sslFiles(<service>_crt)).
func (p *certPaths) svcCrt(name string) string {
	switch name {
	case certSvcLDAP:
		return filepath.Join(p.confDir, "slapd.crt")
	case certSvcMTA:
		return filepath.Join(p.confDir, "smtpd.crt")
	case certSvcProxy:
		return filepath.Join(p.confDir, "nginx.crt")
	default:
		return p.mailboxdPEM
	}
}

// svcKey is the key file of a service.
func (p *certPaths) svcKey(name string) string {
	return strings.TrimSuffix(p.svcCrt(name), ".crt") + ".key"
}

// Names of the directories under the base directory, and a message used more than once.
const (
	certDirConf     = "conf"
	certDirSSL      = "ssl"
	certDirCarbonio = "carbonio"

	certMsgOpenInput = "open input '%s' failed: %s\n"
)

// certErrText is the text of $! for err: the system error, capitalised.
func certErrText(err error) string {
	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		err = pe.Err
	}

	if le, ok := errors.AsType[*os.LinkError](err); ok {
		err = le.Err
	}

	s := err.Error()
	r, n := utf8.DecodeRuneInString(s)

	return string(unicode.ToUpper(r)) + s[n:]
}

func (m *certMgr) stat(name string) (fs.FileInfo, error) { return m.e.Sys.FS.Stat(name) }

// isFile is -f.
func (m *certMgr) isFile(name string) bool {
	fi, err := m.stat(name)

	return err == nil && fi.Mode().IsRegular()
}

// isDir is -d.
func (m *certMgr) isDir(name string) bool {
	fi, err := m.stat(name)

	return err == nil && fi.IsDir()
}

// exists is -e.
func (m *certMgr) exists(name string) bool {
	_, err := m.stat(name)

	return err == nil
}

// hasData is -s: the file exists and is not empty.
func (m *certMgr) hasData(name string) bool {
	fi, err := m.stat(name)

	return err == nil && fi.Size() > 0
}

// readable is -r.
func (m *certMgr) readable(name string) bool {
	f, err := m.e.Sys.FS.Open(name)
	if err != nil {
		return false
	}

	_ = f.Close()

	return true
}

// checkFiles is _checkFiles: every file must be readable.
func (m *certMgr) checkFiles(files ...string) bool {
	ok := true

	for _, f := range files {
		if !m.readable(f) {
			m.fail("Can't read file '%s'\n", f)

			ok = false
		}
	}

	return ok
}

// slurp reads a file.
func (m *certMgr) slurp(name string) ([]byte, error) { return m.e.Sys.FS.ReadFile(name) }

// errCertSymlink is why root refuses a link: the trees the script works in
// belong to zextras, which could point a link at a file only root can reach.
var errCertSymlink = errors.New("is a symbolic link: refusing to follow it as root")

// opOpen is the operation of the errors of an open.
const opOpen = "open"

// maxLinks is how many symbolic links in a row are followed.
const maxLinks = 8

// noFollow tells whether the run must not follow symbolic links: root does
// not, zextras may (its own links, such as one to a Let's Encrypt key, give
// it nothing it does not have).
func (m *certMgr) noFollow() bool { return !m.e.IsZextras }

// rootOnlyDir tells whether only root can change a directory: it is root's and
// neither its group nor others can write it. What it holds cannot be swapped
// for a link by anyone else. An owner the system does not tell is not root's.
func (m *certMgr) rootOnlyDir(dir string) bool {
	fi, err := m.e.Sys.FS.Stat(dir)
	if err != nil {
		return false
	}

	uid, _, known := fileOwner(fi)

	return known && uid == 0 && fi.Mode().Perm()&0o022 == 0
}

// resolve follows the symbolic links at name and returns the path of what is
// at the end of them (which may not exist yet). root follows the links that
// are in a directory only root can change, such as the one of the JDK to its
// cacerts, and refuses the others: a link a user can plant is not followed.
func (m *certMgr) resolve(name string) (string, error) {
	for range maxLinks {
		fi, err := m.e.Sys.FS.Lstat(name)
		if isLink := err == nil && fi.Mode()&fs.ModeSymlink != 0; !isLink {
			return name, nil // a file, or nothing yet: what is to be opened
		}

		dir := filepath.Dir(name)
		if m.noFollow() && !m.rootOnlyDir(dir) {
			return "", &fs.PathError{Op: opOpen, Path: name, Err: errCertSymlink}
		}

		target, err := m.e.Sys.FS.Readlink(name)
		if err != nil {
			return "", err
		}

		name = target
		if !filepath.IsAbs(name) {
			name = filepath.Join(dir, target)
		}
	}

	return "", &fs.PathError{Op: opOpen, Path: name, Err: syscall.ELOOP}
}

// openFile opens name; as root a symbolic link that a user could have planted
// at name is an error, both when it is there before the open and when it is
// swapped in after the check.
func (m *certMgr) openFile(name string, flag int, perm fs.FileMode) (sys.File, error) {
	if m.noFollow() {
		resolved, err := m.resolve(name)
		if err != nil {
			return nil, err
		}

		name = resolved
		flag |= syscall.O_NOFOLLOW
	}

	f, err := m.e.Sys.FS.OpenFile(name, flag, perm)
	if errors.Is(err, syscall.ELOOP) {
		return nil, &fs.PathError{Op: opOpen, Path: name, Err: errCertSymlink}
	}

	return f, err
}

// slurpOwn reads a file of the script's own trees: as root without following
// a symbolic link.
func (m *certMgr) slurpOwn(name string) ([]byte, error) {
	if !m.noFollow() {
		return m.slurp(name)
	}

	f, err := m.openFile(name, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}

	defer func() { _ = f.Close() }()

	return io.ReadAll(f)
}

// chownZextras gives a directory this run created to zextras when the
// process runs as root; zextras' own files already belong to it. A link is
// given away itself, never what it points to.
func (m *certMgr) chownZextras(name string) error {
	if m.e.IsZextras {
		return nil
	}

	if err := m.loadIDs(); err != nil {
		return err
	}

	return m.e.Sys.FS.Lchown(name, m.zuid, m.zgid)
}

// initNew gives a file this run just created its mode and, as root, its
// owner, through the descriptor and before any data is written: a key is
// never on disk with the wrong owner or mode, and no path can be redirected.
func (m *certMgr) initNew(f sys.File, perm fs.FileMode) error {
	if err := f.Chmod(perm); err != nil {
		return err
	}

	if m.e.IsZextras {
		return nil
	}

	if err := m.loadIDs(); err != nil {
		return err
	}

	return f.Chown(m.zuid, m.zgid)
}

func (m *certMgr) loadIDs() error {
	if !m.idsLoaded {
		m.zuid, m.zgid, m.idsErr = zextrasIDs(m.e)
		m.idsLoaded = true
	}

	return m.idsErr
}

// writeFile is open(">") of the script: an existing file keeps its inode,
// mode and owner and is truncated; a new one gets perm (whatever the umask of
// the process is) and, when the process is root, zextras as owner.
func (m *certMgr) writeFile(name string, data []byte, perm fs.FileMode) error {
	return m.putFile(name, data, perm, os.O_TRUNC)
}

// appendFile is open(">>").
func (m *certMgr) appendFile(name string, data []byte, perm fs.FileMode) error {
	return m.putFile(name, data, perm, os.O_APPEND)
}

func (m *certMgr) putFile(name string, data []byte, perm fs.FileMode, mode int) error {
	name, err := m.resolve(name)
	if err != nil {
		return err
	}

	_, lstatErr := m.e.Sys.FS.Lstat(name)
	created := lstatErr != nil

	f, err := m.openFile(name, os.O_WRONLY|os.O_CREATE|mode, perm)
	if err != nil {
		return err
	}

	if created {
		err = m.initNew(f, perm)
	}

	if err == nil {
		_, err = f.Write(data)
	}

	if cerr := f.Close(); err == nil {
		err = cerr
	}

	return err
}

// mkdirAll is File::Path::make_path: the directories it creates get perm and
// belong to zextras.
func (m *certMgr) mkdirAll(dir string, perm fs.FileMode) error {
	var missing []string

	for d := dir; !m.exists(d); d = filepath.Dir(d) {
		missing = append(missing, d)

		if d == filepath.Dir(d) {
			break
		}
	}

	if err := m.e.Sys.FS.MkdirAll(dir, perm); err != nil {
		return err
	}

	for _, d := range slices.Backward(missing) {
		if err := m.e.Sys.FS.Chmod(d, perm); err != nil {
			return err
		}

		if err := m.chownZextras(d); err != nil {
			return err
		}
	}

	return nil
}

// sameFile tells whether two names are one file.
func (m *certMgr) sameFile(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}

	fa, ea := m.stat(a)
	fb, eb := m.stat(b)

	return ea == nil && eb == nil && os.SameFile(fa, fb)
}

// copyFile is _copy: File::Copy::copy, which does not carry the mode over;
// copying a file over itself is not an error. The source is a file of the
// script's own trees, so root does not follow a link there.
func (m *certMgr) copyFile(from, to string, perm fs.FileMode) bool {
	return m.copyWith(from, to, perm, m.slurpOwn)
}

// copyGiven is copyFile for a source the operator named on the command line:
// a link there (a Let's Encrypt live/ file, say) is what was asked for.
func (m *certMgr) copyGiven(from, to string, perm fs.FileMode) bool {
	return m.copyWith(from, to, perm, m.slurp)
}

func (m *certMgr) copyWith(from, to string, perm fs.FileMode, read func(string) ([]byte, error)) bool {
	m.say("** Copying '%s' to '%s'\n", from, to)

	if m.sameFile(from, to) && m.exists(from) {
		return true
	}

	data, err := read(from)
	if err == nil {
		err = m.writeFile(to, data, perm)
	}

	if err != nil {
		return m.fail("copy '%s' to '%s' failed: %s\n", from, to, certErrText(err))
	}

	return true
}

// certLines splits data into lines, each ending in a newline ($l =~
// s/\r?\n?$/\n/ on every line, so a missing newline is added and a CRLF
// becomes LF).
func certLines(data []byte) []string {
	var lines []string

	for len(data) > 0 {
		line := data
		rest := []byte(nil)

		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, rest = data[:i], data[i+1:]
		}

		lines = append(lines, strings.TrimSuffix(string(line), "\r")+"\n")
		data = rest
	}

	return lines
}

// certConcat is getFromConcatData: the certificates of concatenated PEM data,
// each from its BEGIN line to its END line, with LF line ends.
func certConcat(data string) []string {
	var (
		certs []string
		cur   strings.Builder
		in    bool
	)

	for _, l := range certLines([]byte(data)) {
		if strings.Contains(l, "BEGIN CERTIFICATE") {
			in = true
		}

		if in {
			cur.WriteString(l)
		}

		if strings.Contains(l, "END CERTIFICATE") {
			in = false

			certs = append(certs, cur.String())
			cur.Reset()
		}
	}

	return certs
}

var (
	certBeginRE = regexp.MustCompile(`^-{5}BEGIN CERTIFICATE-{5}\s*$`)
	certEndRE   = regexp.MustCompile(`^-{5}END CERTIFICATE-{5}\s*$`)
)

// fixNewline is _fixNewline: files with DOS line ends or no newline at the
// end are rewritten in place; the original is kept as <file>.bak.
func (m *certMgr) fixNewline(files ...string) bool {
	for _, f := range files {
		data, err := m.slurp(f)
		if err != nil {
			return m.fail(certMsgOpenInput, f, certErrText(err))
		}

		if !certNeedsFix(data) {
			continue
		}

		m.say("** Fixing newlines in '%s'\n", f)

		if err := m.rewrite(f, ".bak", []byte(strings.Join(certLines(data), ""))); err != nil {
			return m.fail("rewrite '%s' failed: %s\n", f, certErrText(err))
		}
	}

	return true
}

// certNeedsFix is true when a line ends in CRLF or the last one has no
// newline.
func certNeedsFix(data []byte) bool {
	if len(data) > 0 && data[len(data)-1] != '\n' {
		return true
	}

	return bytes.Contains(data, []byte("\r\n"))
}

// rewrite is perl -i<suffix>: the old content goes to <file><suffix>, the new
// one replaces it in the file.
func (m *certMgr) rewrite(name, suffix string, data []byte) error {
	old, err := m.slurp(name)
	if err != nil {
		return err
	}

	mode := fs.FileMode(certModeFile)
	if fi, err := m.stat(name); err == nil {
		mode = fi.Mode().Perm()
	}

	if err := m.writeFile(name+suffix, old, mode); err != nil {
		return err
	}

	return m.writeFile(name, data, mode)
}

// keepFirstCert is _keepFirstCert: a file with more than one certificate is
// cut after the first (the original goes to <file>.orig).
func (m *certMgr) keepFirstCert(file string) bool {
	data, err := m.slurp(file)
	if err != nil {
		return m.fail(certMsgOpenInput, file, certErrText(err))
	}

	var (
		lines = strings.SplitAfter(string(data), "\n")
		found int
	)

	for _, l := range lines {
		if certBeginRE.MatchString(strings.TrimRight(l, "\n")) {
			found++
		}
	}

	if found < 2 {
		return true
	}

	m.say("** Keeping first certificate in '%s'\n", file)

	var keep strings.Builder

	for _, l := range lines {
		keep.WriteString(l)

		if certEndRE.MatchString(strings.TrimRight(l, "\n")) {
			break
		}
	}

	if err := m.rewrite(file, ".orig", []byte(keep.String())); err != nil {
		return m.fail("rewrite '%s' failed: %s\n", file, certErrText(err))
	}

	return true
}

// appendChain is _append: the lines of from are added to to.
func (m *certMgr) appendChain(from, to string) bool {
	m.say("** Appending ca chain '%s' to '%s'\n", from, to)

	if _, err := m.stat(from); err != nil {
		return m.fail("stat '%s' failed: %s\n", from, certErrText(err))
	}

	if _, err := m.stat(to); err != nil {
		return m.fail("stat '%s' failed: %s\n", to, certErrText(err))
	}

	if m.sameFile(from, to) {
		return m.fail("'%s' and '%s' are identical\n", from, to)
	}

	data, err := m.slurp(from)
	if err != nil {
		return m.fail(certMsgOpenInput, to, certErrText(err))
	}

	if err := m.appendFile(to, []byte(strings.Join(certLines(data), "")), certModeFile); err != nil {
		return m.fail("open output '%s' failed: %s\n", to, certErrText(err))
	}

	return true
}

// hashLink is makeCAHashLink: a symlink <subject hash>.<n> next to the
// certificate file, as OpenSSL looks CA certificates up by hash.
func (m *certMgr) hashLink(file string) bool {
	data, err := m.slurp(file)
	if err != nil {
		return m.fail("openssl x509 -hash failed(1):\n%s\n", certErrText(err))
	}

	certs, err := certParseCerts(data)
	if err != nil || len(certs) == 0 {
		return m.fail("openssl x509 -hash failed(1):\nCould not read certificate from %s\n", file)
	}

	dn, err := certParseDN(certs[0].RawSubject)
	if err == nil {
		var hash string

		if hash, err = dn.canonHash(); err == nil {
			return m.symlinkHash(file, hash)
		}
	}

	return m.fail("openssl x509 -hash failed(1):\n%s\n", err)
}

func (m *certMgr) symlinkHash(file, hash string) bool {
	dir, name := filepath.Split(file)

	ext := 0
	for m.exists(dir + hash + "." + strconv.Itoa(ext)) {
		ext++
	}

	link := hash + "." + strconv.Itoa(ext)
	m.say("** Creating CA hash symlink '%s' -> '%s'\n", link, name)

	if err := m.symlink(name, dir+link); err != nil {
		return m.fail("create symlink '%s' -> '%s' failed\n", link, name)
	}

	return true
}

// symlink creates a link that, like everything the script makes, belongs to
// zextras (the link itself: lchown).
func (m *certMgr) symlink(target, link string) error {
	if err := m.e.Sys.FS.Symlink(target, link); err != nil {
		return err
	}

	if m.e.IsZextras {
		return nil
	}

	if err := m.loadIDs(); err != nil {
		return err
	}

	return m.e.Sys.FS.Lchown(link, m.zuid, m.zgid)
}

// installCA is installCA: the deployed CA directory is emptied and gets the
// CA key and certificate and, split into one file per certificate, the
// commercial CA chain, each with its hash link.
func (m *certMgr) installCA() (bool, error) {
	m.debugf("installCA\n")

	if !m.isDir(m.p.confCA) {
		return false, fmt.Errorf("CA config dir '%s': No such file or directory", m.p.confCA)
	}

	if ok, err := m.cleanCAConf(); !ok || err != nil {
		return false, err
	}

	m.say("** Copying CA to %s\n", m.p.confCA)

	failed := false

	for _, c := range []struct {
		from, to string
		perm     fs.FileMode
	}{{m.p.caKey, m.p.confCAKey, certModeFile}, {m.p.caCrt, m.p.confCACrt, certModePublic}} {
		if m.hasData(c.from) && !m.copyFile(c.from, c.to, c.perm) {
			failed = true
		}
	}

	if !failed && m.hasData(m.p.confCACrt) && !m.hashLink(m.p.confCACrt) {
		failed = true
	}

	if !failed && m.hasData(m.p.commCACrt) && !m.splitCommercial() {
		failed = true
	}

	return !failed, nil
}

// cleanCAConf removes the files of the deployed CA directory.
func (m *certMgr) cleanCAConf() (bool, error) {
	entries, err := m.e.Sys.FS.ReadDir(m.p.confCA)
	if err != nil {
		return false, fmt.Errorf("installCA: opendir '%s' failed: %s", m.p.confCA, certErrText(err))
	}

	var remove []string

	for _, ent := range entries {
		if strings.HasPrefix(ent.Name(), ".") {
			continue
		}

		name := filepath.Join(m.p.confCA, ent.Name())
		if fi, err := m.e.Sys.FS.Lstat(name); err == nil && (fi.Mode().IsRegular() || fi.Mode()&fs.ModeSymlink != 0) {
			remove = append(remove, name)
		}
	}

	if len(remove) == 0 {
		return true, nil
	}

	m.say("** Cleaning up %d files from '%s'\n", len(remove), m.p.confCA)

	for _, f := range remove {
		m.say("** Removing %s\n", f)

		if !m.unlink(f) {
			return false, nil
		}
	}

	return true, nil
}

// splitCommercial breaks the commercial CA chain into commercial_ca_<n>.crt
// files in the deployed CA directory.
func (m *certMgr) splitCommercial() bool {
	data, err := m.slurp(m.p.commCACrt)
	if err != nil {
		return m.fail("installCA: open '%s' failed: %s\n", m.p.commCACrt, certErrText(err))
	}

	name := strings.TrimSuffix(filepath.Base(m.p.commCACrt), filepath.Ext(m.p.commCACrt))
	failed := false

	for i, c := range certConcat(string(data)) {
		out := filepath.Join(m.p.confCA, name+"_"+strconv.Itoa(i+1)+filepath.Ext(m.p.commCACrt))
		m.say("** Creating %s\n", out)

		if err := m.writeFile(out, []byte(c), certModePublic); err != nil {
			m.fail("installCA: open '%s' failed: %s\n", out, certErrText(err))

			failed = true

			continue
		}

		if !m.hashLink(out) {
			failed = true
		}
	}

	return !failed
}

// createConf is createConf: it renders conf/zmssl.cnf.in into ofile with the
// host name, the digest and the subject alternative names. The certificates
// themselves are made natively, from the same settings, so the file is only
// kept for the tools that read it; without the template nothing is written.
func (m *certMgr) createConf(ofile string, altnames ...string) {
	key := ofile + "\x00" + strings.Join(altnames, "\x00")
	if m.confMemoed && m.confMemo == key {
		return
	}

	m.confMemo, m.confMemoed = key, true

	names := certUniqueNames(altnames)

	san := ""
	if len(names) > 0 {
		san = "subjectAltName = DNS:" + strings.Join(names, ",DNS:")
	}

	tpl, err := m.slurp(m.p.confInput)
	if err != nil {
		m.debugf("createConf: no template '%s'\n", m.p.confInput)

		return
	}

	verb := "C"
	if m.isFile(ofile) {
		verb = "Rec"
	}

	m.say("** %sreating %s\n", verb, ofile)

	var out strings.Builder

	for _, l := range strings.SplitAfter(string(tpl), "\n") {
		l = strings.Replace(l, "@@HOSTNAME@@", m.lc["zimbra_server_hostname"], 1)
		l = strings.Replace(l, "@@ssl_default_digest@@", m.digest, 1)
		l = strings.Replace(l, "@@SUBJECT_ALT_NAMES@@", san, 1)
		out.WriteString(l)
	}

	if err := m.writeFile(ofile, []byte(out.String()), certModeFile); err != nil {
		m.fail("createConf: open '%s' failed: %s\n", ofile, certErrText(err))
	}
}

// certUniqueNames drops the names that repeat, whatever their case, keeping
// the order and the first spelling.
func certUniqueNames(names []string) []string {
	var (
		out  []string
		seen = map[string]bool{}
	)

	for _, n := range names {
		if l := strings.ToLower(n); !seen[l] {
			seen[l] = true

			out = append(out, n)
		}
	}

	return out
}

// backupSSLDirs is backupSSLDirs: once per run, ssl/carbonio is copied to
// ssl/carbonio.<YYYYMMDDHHMMSS>. The copy keeps the modes and links but not
// the times (cp -pr did).
func (m *certMgr) backupSSLDirs() bool {
	if m.backupDone {
		return true
	}

	m.backupDone = true

	dir := m.p.ssl
	if !m.isDir(dir) {
		return true
	}

	dest := dir + "." + m.e.Sys.Clock.Now().Local().Format("20060102150405")
	m.say("** Backup %s to %s\n", dir, dest)

	if err := m.copyTree(dir, dest); err != nil {
		return m.fail("backup '%s' to '%s' returned non-zero(1)\n", dir, dest)
	}

	return true
}

// copyTree copies a directory tree: modes and symlinks are kept.
func (m *certMgr) copyTree(src, dst string) error {
	return m.e.Sys.FS.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, _ := filepath.Rel(src, path)

		return m.copyEntry(path, filepath.Join(dst, rel), d.IsDir())
	})
}

// copyEntry copies one entry of a tree: a directory, a symlink or a file.
func (m *certMgr) copyEntry(from, to string, isDir bool) error {
	fi, err := m.e.Sys.FS.Lstat(from)
	if err != nil {
		return err
	}

	switch {
	case isDir:
		return m.mkdirAll(to, fi.Mode().Perm())
	case fi.Mode()&fs.ModeSymlink != 0:
		target, err := m.e.Sys.FS.Readlink(from)
		if err != nil {
			return err
		}

		return m.symlink(target, to)
	}

	data, err := m.slurp(from)
	if err != nil {
		return err
	}

	return m.writeFile(to, data, fi.Mode().Perm())
}

// checkOwner is _checkStat: a file or directory that does not belong to
// zextras is reported, but the run goes on.
func (m *certMgr) checkOwner(tag, item string) {
	fi, err := m.stat(item)
	if err != nil {
		return
	}

	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return
	}

	zu := os.Geteuid()

	if !m.e.IsZextras {
		if m.loadIDs() != nil {
			return
		}

		zu = m.zuid
	}

	if int(st.Uid) == zu {
		return
	}

	owner := strconv.Itoa(int(st.Uid))
	if u, err := user.LookupId(owner); err == nil {
		owner = u.Username
	}

	m.fail("%s: owner '%s' != 'zextras' (%d!=%d) for '%s'\n", tag, owner, st.Uid, zu, item)
}

// initSSLDirs is initSSLDirs: once per run the directories exist and
// index.txt is there; the cacerts and keystore of a deploy are checked.
func (m *certMgr) initSSLDirs() bool {
	if m.initDone {
		return true
	}

	m.initDone = true
	m.debugf("initSSLDirs\n")

	const tag = "initSSLDirs"

	failed := false

	for _, dir := range []string{m.p.confCA, m.p.ssl, m.p.caDir, m.p.commDir, m.p.selfDir, m.p.newcerts} {
		if !m.initDir(tag, dir) {
			failed = true
		}
	}

	if !m.touchIndex(tag) {
		failed = true
	}

	if strings.HasPrefix(m.o.Command, "deploy") {
		for _, f := range []string{m.p.cacerts, m.p.keystore} {
			if f != "" && m.isFile(f) {
				m.checkOwner(tag, f)
			}
		}
	}

	return !failed
}

// unlink removes a file, saying so when it cannot.
func (m *certMgr) unlink(f string) bool {
	if m.e.Sys.FS.Remove(f) != nil {
		return m.fail("unlinking '%s' failed\n", f)
	}

	return true
}

// initDir makes sure a directory is there (conf/ca is also made readable by
// all).
func (m *certMgr) initDir(tag, dir string) bool {
	if m.isDir(dir) {
		m.checkOwner(tag, dir)
	} else {
		m.say("** Creating directory '%s'\n", dir)

		if err := m.mkdirAll(dir, certModeDir); err != nil {
			return m.fail("%s: mkdir '%s' failed: %s\n", tag, dir, certErrText(err))
		}
	}

	if dir != m.p.confCA {
		return true
	}

	if err := m.e.Sys.FS.Chmod(dir, certModeCADir); err != nil {
		return m.fail("%s: chmod(0755) '%s' failed: %s\n", tag, dir, certErrText(err))
	}

	return true
}

// touchIndex creates an empty index.txt when there is none.
func (m *certMgr) touchIndex(tag string) bool {
	if m.isFile(m.p.index) {
		m.checkOwner(tag, m.p.index)

		return true
	}

	m.say("** Touching file '%s'\n", m.p.index)

	if err := m.appendFile(m.p.index, nil, certModeFile); err != nil {
		_, _ = fmt.Fprintf(m.errw, "%s: touch '%s' failed: %s\n", tag, m.p.index, certErrText(err))

		return false
	}

	return true
}

// migrate is migrate: the files of the old ssl/ssl tree are copied to the
// current one.
func (m *certMgr) migrate() (bool, error) {
	old := filepath.Join(m.e.Paths.Base, certDirSSL, certDirSSL)
	if !m.isDir(old) {
		m.say("** No '%s' directory. Nothing to migrate.\n", old)

		return false, nil
	}

	if !m.initSSLDirs() {
		return false, nil
	}

	m.say("** Migrating ssl certs from '%s' to '%s'\n", old, m.p.ssl)

	// Sorted by the key names of the script.
	for _, c := range []struct{ from, to string }{
		{"ca/ca.pem", m.p.caCrt}, {"ca/ca.csr", filepath.Join(m.p.caDir, "ca.csr")},
		{"ca/ca.key", m.p.caKey}, {"ca/ca.srl", m.p.caSrl},
		{"server/mailboxd.crt", m.p.comm.crt}, {"server/mailboxd.csr", m.p.comm.csr},
		{"server/server.crt", m.p.self.crt}, {"server/server.csr", m.p.self.csr},
		{"server/server.key", m.p.self.key},
	} {
		from := filepath.Join(old, c.from)
		if m.isFile(from) && !m.copyFile(from, c.to, certModeFile) {
			return false, nil
		}
	}

	return true, nil
}
