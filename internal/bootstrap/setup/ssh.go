// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/zextras/carbonio-configd/internal/config"
)

const (
	sshKeyAttr    = "zimbraSshPublicKey"
	sshKeyBits    = 2048
	sshKeyFile    = "zimbra_identity"
	sshAuthFile   = "authorized_keys"
	sshDirMode    = 0o700
	sshPrivMode   = 0o600
	sshPubMode    = 0o644
	sshAuthMode   = 0o644
	sshPEMType    = "RSA PRIVATE KEY"
	sshHostField  = 4 // the host comment of "command=... ssh-rsa <key> <host>"
	sshKeyComment = 3 // and of the key "ssh-rsa <key> <host>"
)

// StepZmsshkeygen ports setup.pl applyConfig L6011-6013: zmsshkeygen on a new
// install, done natively: a 2048-bit RSA key pair for the zextras user,
// written like ssh-keygen -m PEM does (PKCS#1 "RSA PRIVATE KEY", no
// passphrase) as .ssh/zimbra_identity and zimbra_identity.pub with the server
// name as comment, then the public key stored in zimbraSshPublicKey of the
// server entry (zmprov ms) through the LDAP client. The previous key is
// replaced. It regenerates the key on a re-run of an unfinished new install
// (cheap, so no Done). Optional: setup.pl ignored the status.
func StepZmsshkeygen() Step {
	return Step{
		Title:    "Generate zextras SSH key",
		Optional: true,
		When:     func(e *Env) bool { return e.Facts.NewInstall },
		Apply:    GenerateSSHKey,
	}
}

// StepZmupdateauthkeys ports setup.pl applyConfig L6014-6018:
// zmupdateauthkeys on every run, done natively: for every server that has a
// zimbraSshPublicKey, .ssh/authorized_keys loses the lines of that server's
// host and gets `command="<home>/libexec/zmrcd" <key>` appended, in the format
// the script wrote (the other nodes still run it). The remote mode (-a) is not
// ported. Optional: setup.pl ignored the status.
func StepZmupdateauthkeys() Step {
	return Step{
		Title:    "Update authorized SSH keys",
		Optional: true,
		Apply:    UpdateAuthKeys,
	}
}

// GenerateSSHKey is the work of zmsshkeygen (see StepZmsshkeygen): the host
// name, zextras ids and LDAP access come from the node's localconfig, so the
// setup.pl command shares it with the step.
func GenerateSSHKey(e *Env) error {
	host, err := e.LCValue(svcLocalConfigHost)
	if err != nil {
		return err
	}

	if host == "" {
		return errors.New("zimbra_server_hostname is not set in localconfig")
	}

	uid, gid, err := sshZextrasIDs(e)
	if err != nil {
		return err
	}

	key, err := rsa.GenerateKey(rand.Reader, sshKeyBits)
	if err != nil {
		return fmt.Errorf("generating the RSA key: %w", err)
	}

	pub, err := ssh.NewPublicKey(&key.PublicKey)
	if err != nil {
		return fmt.Errorf("encoding the public key: %w", err)
	}

	pubLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))) + " " + host
	priv := pem.EncodeToMemory(&pem.Block{Type: sshPEMType, Bytes: x509.MarshalPKCS1PrivateKey(key)})

	dir := e.Paths.Path(".ssh")
	if err := sshEnsureDir(e, dir, uid, gid); err != nil {
		return err
	}

	keyPath := dir + "/" + sshKeyFile

	if err := sshWriteOwned(e, keyPath, priv, sshPrivMode, uid, gid); err != nil {
		return err
	}

	if err := sshWriteOwned(e, keyPath+".pub", []byte(pubLine+"\n"), sshPubMode, uid, gid); err != nil {
		return err
	}

	return sshPublishKey(e, host, pubLine)
}

// sshPublishKey sets zimbraSshPublicKey of the server entry.
func sshPublishKey(e *Env, host, pubLine string) error {
	d, err := svcOpenDir(e)
	if err != nil {
		return err
	}

	defer func() { _ = d.Close() }()

	if err := d.SetServerAttribute(host, sshKeyAttr, pubLine); err != nil {
		return fmt.Errorf("setting %s of server %s: %w", sshKeyAttr, host, err)
	}

	e.Log.Detail("Stored the SSH public key in %s of server %s", sshKeyAttr, host)

	return nil
}

// UpdateAuthKeys is the work of zmupdateauthkeys (see StepZmupdateauthkeys),
// shared with the setup.pl command.
func UpdateAuthKeys(e *Env) error {
	path := e.Paths.Path(".ssh", sshAuthFile)

	current, err := e.Sys.FS.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	uid, gid, err := sshZextrasIDs(e)
	if err != nil {
		return err
	}

	d, err := svcOpenDir(e)
	if err != nil {
		return err
	}

	defer func() { _ = d.Close() }()

	keys, err := d.ServerAttribute(sshKeyAttr)
	if err != nil {
		return fmt.Errorf("reading %s of the servers: %w", sshKeyAttr, err)
	}

	lines := sshSplitKeys(string(current))
	zmrcd := `command="` + e.Paths.Path("libexec", "zmrcd") + `" `

	for _, host := range slices.Sorted(maps.Keys(keys)) {
		key := strings.TrimSpace(keys[host])
		if key == "" {
			e.Log.Detail("Key for %s NOT FOUND", host)

			continue
		}

		e.Log.Detail("Updating keys for %s", host)

		lines = append(sshDropHost(lines, key), zmrcd+key)
	}

	return sshWriteOwned(e, path, []byte(strings.Join(lines, "\n")+"\n"), sshAuthMode, uid, gid)
}

// UpdateAuthKeysEverywhere is zmupdateauthkeys -a: it first asks every
// server, through zmrc, to run zmupdateauthkeys itself, then updates this
// node. A server that cannot be reached is logged, as the script went on.
func UpdateAuthKeysEverywhere(e *Env) error {
	d, err := svcOpenDir(e)
	if err != nil {
		return err
	}

	keys, err := d.ServerAttribute(sshKeyAttr)
	_ = d.Close()

	if err != nil {
		return fmt.Errorf("listing the servers: %w", err)
	}

	zmrc := e.Paths.Path("libexec", "zmrc")

	for _, host := range slices.Sorted(maps.Keys(keys)) {
		e.Log.Detail("Updating authkeys on remote server %s", host)

		if err := e.Run(&Cmd{Path: zmrc, Args: []string{host},
			Stdin: strings.NewReader("HOST:" + host + " zmupdateauthkeys\n")}); err != nil {
			e.Log.Detail("Updating authkeys on remote server %s failed: %v", host, err)
		}
	}

	return UpdateAuthKeys(e)
}

// sshSplitKeys returns the lines of an authorized_keys file the way the
// script's `$(cat file)` and `read -r` saw them: no trailing blank lines,
// each line without its surrounding white space.
func sshSplitKeys(content string) []string {
	content = strings.TrimRight(content, "\n")
	if content == "" {
		return nil
	}

	lines := strings.Split(content, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}

	return lines
}

// sshDropHost removes the lines whose 4th field is the host comment (3rd
// field) of key, as zmupdateauthkeys replacePubKey does.
func sshDropHost(lines []string, key string) []string {
	f := strings.Fields(key)
	if len(f) < sshKeyComment {
		// awk '{print $3}' printed "": it only matches the lines without a 4th field.
		f = append(f, make([]string, sshKeyComment-len(f))...)
	}

	host := f[sshKeyComment-1]

	return slices.DeleteFunc(slices.Clone(lines), func(l string) bool {
		lf := strings.Fields(l)

		var linehost string
		if len(lf) >= sshHostField {
			linehost = lf[sshHostField-1]
		}

		return linehost == host
	})
}

// sshZextrasIDs are the uid and gid of the zextras user as the node's
// localconfig has them (zimbra_uid, zimbra_gid: the configLCValues step stores
// them).
func sshZextrasIDs(e *Env) (uid, gid int, err error) {
	for _, q := range []struct {
		key string
		dst *int
	}{{"zimbra_uid", &uid}, {"zimbra_gid", &gid}} {
		v, err := e.LCValue(q.key)
		if err != nil {
			return 0, 0, err
		}

		n, convErr := strconv.Atoi(strings.TrimSpace(v))
		if convErr != nil || n < 0 {
			return 0, 0, fmt.Errorf("localconfig %s is %q, not the id of %s", q.key, v, config.ZextrasUser)
		}

		*q.dst = n
	}

	return uid, gid, nil
}

// sshEnsureDir makes the .ssh directory: mode 0700, owned by zextras.
func sshEnsureDir(e *Env, dir string, uid, gid int) error {
	if err := e.Sys.FS.MkdirAll(dir, sshDirMode); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	if err := e.Sys.FS.Chmod(dir, sshDirMode); err != nil {
		return fmt.Errorf("chmod %s: %w", dir, err)
	}

	if err := e.Sys.FS.Chown(dir, uid, gid); err != nil {
		return fmt.Errorf("chown %s: %w", dir, err)
	}

	return nil
}

// sshWriteOwned replaces name atomically with data, then gives it to zextras.
func sshWriteOwned(e *Env, name string, data []byte, perm fs.FileMode, uid, gid int) error {
	if err := e.Sys.FS.WriteFileAtomic(name, data, perm); err != nil {
		return fmt.Errorf("writing %s: %w", name, err)
	}

	if err := e.Sys.FS.Chown(name, uid, gid); err != nil {
		return fmt.Errorf("chown %s: %w", name, err)
	}

	return nil
}
