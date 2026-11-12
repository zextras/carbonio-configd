// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"slices"
	"strings"
)

// dhPEMType is the PEM block of PKCS#3 DH parameters, what openssl dhparam
// writes.
const dhPEMType = "DH PARAMETERS"

// dhProg is the program name the messages of zmdhparam carry.
const dhProg = "zmdhparam"

// dhGroupPrimes are the groups "set -new" can give, by size in bits: the
// RFC 7919 finite field groups, whose generator is 2.
var dhGroupPrimes = map[int]string{2048: dhPrime2048, 3072: dhPrime3072, 4096: dhPrime4096}

// dhGroupSizes are the sizes of dhGroupPrimes, ascending.
func dhGroupSizes() []int {
	sizes := make([]int, 0, len(dhGroupPrimes))
	for bits := range dhGroupPrimes {
		sizes = append(sizes, bits)
	}

	slices.Sort(sizes)

	return sizes
}

// dhGroupPEM returns the RFC 7919 ffdhe<bits> group as the PEM "DH PARAMETERS"
// block openssl dhparam writes: the PKCS#3 DER SEQUENCE { prime, generator }.
func dhGroupPEM(bits int) ([]byte, error) {
	hex, ok := dhGroupPrimes[bits]
	if !ok {
		return nil, fmt.Errorf("no ffdhe%d group", bits)
	}

	p, ok := new(big.Int).SetString(hex, 16)
	if !ok {
		return nil, fmt.Errorf("invalid ffdhe%d prime", bits)
	}

	der, err := asn1.Marshal(dhParams{P: p, G: big.NewInt(2)})
	if err != nil {
		return nil, fmt.Errorf("encoding the dhparam group: %w", err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: dhPEMType, Bytes: der}), nil
}

// dhParams is PKCS#3 DHParameter.
type dhParams struct {
	P, G *big.Int
	L    int `asn1:"optional"`
}

// dhMillerRabin is the rounds of the primality test of the prime of a
// parameter set (openssl dhparam -check uses the same order).
const dhMillerRabin = 20

// dhCheckPEM is "openssl dhparam -check" over data: it finds the first DH
// PARAMETERS block (what precedes it, like openssl's parameter files may hold
// besides, is skipped), checks that its prime is a prime and the generator is
// in 2..p-2, and returns the block as canonical PEM. Unlike openssl, which
// only warns, a prime that is not a prime is an error. The strength of the
// parameters is not checked.
func dhCheckPEM(data []byte) ([]byte, error) {
	var block *pem.Block

	for remaining := data; ; {
		block, remaining = pem.Decode(remaining)
		if block == nil {
			return nil, errors.New("no PEM \"" + dhPEMType + "\" block")
		}

		if block.Type == dhPEMType {
			break
		}
	}

	var params dhParams

	if trailing, err := asn1.Unmarshal(block.Bytes, &params); err != nil || len(trailing) > 0 {
		return nil, errors.New("not PKCS#3 DH parameters")
	}

	two, pMinus2 := big.NewInt(2), new(big.Int)

	if params.P == nil || params.G == nil || params.P.Cmp(big.NewInt(5)) < 0 {
		return nil, errors.New("p value is too small")
	}

	if !params.P.ProbablyPrime(dhMillerRabin) {
		return nil, errors.New("p value is not prime")
	}

	pMinus2.Sub(params.P, two)

	if params.G.Cmp(two) < 0 || params.G.Cmp(pMinus2) > 0 {
		return nil, errors.New("generator g is not in 2..p-2")
	}

	return pem.EncodeToMemory(block), nil
}

// dhSynopsis is the SYNOPSIS of zmdhparam, what -help and the refused options
// print.
const dhSynopsis = "Usage:\n" +
	"    " + dhProg + " <get|set> [options] [-help|-man] [-debug [#]]\n\n" +
	"    Where valid command/option combinations are:\n\n" +
	"      get [-in <file>] [-out <file>]\n" +
	"       :: read PEM format DH params from <LDAP|-in> to <-out|STDOUT>\n\n" +
	"      set <-new [<numbits>]|-in <file>> [-out <file>]\n" +
	"       :: write PEM format DH params from <group|-in> to <LDAP|-out>\n\n" +
	"      -new <numbits>   use the RFC 7919 group of n bits: 2048, 3072 or 4096\n" +
	"                       (default 2048)\n" +
	"      -in  <file>      read PEM format DH params from a file (- is stdin)\n" +
	"      -out <file>      save PEM format DH params to a file (- is stdout)\n" +
	"      -help            this help message\n" +
	"      -man             the complete documentation\n" +
	"      -debug [#]       enable verbose output\n\n" +
	"      zimbraSSLDHParam\n" +
	"       :: LDAP attribute used to store the PEM format DH params\n\n"

// dhNotes is the NOTES section of zmdhparam, printed after the synopsis by -man.
const dhNotes = "NOTES\n" +
	"    DH parameters read via -in are checked for validity, but the strength\n" +
	"    of the parameters is not.\n\n" +
	"    -new does not generate parameters, which took minutes with openssl\n" +
	"    and gave a group nobody had vetted: it uses the RFC 7919 group of that\n" +
	"    size (ffdhe2048, ffdhe3072 or ffdhe4096).\n\n" +
	"    Once zimbraSSLDHParam is updated, zmconfigd is responsible for\n" +
	"    watching for the changed value and then subsequently writing out the\n" +
	"    new value to /opt/zextras/conf/dhparam.pem.\n\n" +
	"    New DH parameters will only take affect once the services that use\n" +
	"    them (Proxy/MTA/LDAP) are restarted.\n\n"

// dhRun is one run of zmdhparam.
type dhRun struct {
	e              *Env
	o              *dhOptions
	stdin          io.Reader
	stdout, stderr io.Writer
}

// RunDHParam is zmdhparam: "get" reads the DH parameters from zimbraSSLDHParam
// (or -in) and prints them (or writes -out); "set" checks the parameters of
// -in, or takes the RFC 7919 group of -new bits, and stores them in
// zimbraSSLDHParam (or writes -out). It returns the exit status, which is the
// script's: 0 when the work was done, 1 otherwise, the usage text and the
// messages on stdout.
func RunDHParam(e *Env, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	p := parseDHParamArgs(args, stderr)
	r := &dhRun{e: e, o: &p.opts, stdin: stdin, stdout: stdout, stderr: stderr}

	switch {
	case p.opts.Man:
		_, _ = io.WriteString(stdout, dhSynopsis+dhNotes)

		return 0
	case p.opts.Help:
		_, _ = io.WriteString(stdout, dhSynopsis)

		return 0
	case !p.parsed:
		_, _ = io.WriteString(stdout, dhSynopsis)

		return 1
	}

	if len(p.errs) > 0 {
		for _, m := range p.errs {
			_, _ = fmt.Fprintf(stdout, "%s: %s\n", dhProg, m)
		}

		_, _ = io.WriteString(stdout, dhSynopsis)

		return 1
	}

	if msgs := r.checkOptions(); len(msgs) > 0 {
		for _, m := range msgs {
			_, _ = fmt.Fprintf(stderr, "%s: ERROR: %s\n", dhProg, m)
		}

		_, _ = io.WriteString(stdout, dhSynopsis)

		return 1
	}

	var err error

	if p.opts.Command == dhCmdGet {
		err = r.get()
	} else {
		err = r.set()
	}

	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: ERROR: %v\n", dhProg, err)

		return 1
	}

	return 0
}

// checkOptions are the problems of the values of the options: the size of
// -new and an -in that cannot be read.
func (r *dhRun) checkOptions() []string {
	var msgs []string

	if r.o.HasNew {
		if msg := r.newBits(); msg != "" {
			msgs = append(msgs, msg)
		}
	}

	if r.o.HasIn {
		if msg := r.readable(r.o.In); msg != "" {
			msgs = append(msgs, msg)
		}
	}

	return msgs
}

// newBits settles the size of -new: no number is the default, below the
// default and sizes without a group are refused. It returns the complaint.
func (r *dhRun) newBits() string {
	o := r.o

	if o.Bits == 0 {
		o.Bits = dhDefaultBits

		r.say("defaulting numbits to '%d'", o.Bits)
	}

	if o.Bits < dhDefaultBits {
		return fmt.Sprintf("numbits '%d' must be >= %d", o.Bits, dhDefaultBits)
	}

	if _, ok := dhGroupPrimes[o.Bits]; !ok {
		return fmt.Sprintf("numbits '%d' is not the size of an RFC 7919 group (%s)", o.Bits, dhSizeList())
	}

	return ""
}

// dhSizeList is "2048, 3072, 4096".
func dhSizeList() string {
	sizes := dhGroupSizes()
	parts := make([]string, len(sizes))

	for i, s := range sizes {
		parts[i] = fmt.Sprint(s)
	}

	return strings.Join(parts, ", ")
}

// readable is the complaint about an -in file that cannot be read.
func (r *dhRun) readable(file string) string {
	if file == "-" {
		return ""
	}

	f, err := r.e.Sys.FS.Open(file)
	if err != nil {
		return fmt.Sprintf("In '%s': unable to read file", file)
	}

	_ = f.Close()

	return ""
}

// say prints a message of the program on stdout.
func (r *dhRun) say(format string, args ...any) {
	_, _ = fmt.Fprintf(r.stdout, "%s: %s\n", dhProg, fmt.Sprintf(format, args...))
}

// debug prints a message on stderr when -debug is on.
func (r *dhRun) debug(format string, args ...any) {
	if r.o.Debug > 0 {
		_, _ = fmt.Fprintf(r.stderr, "DEBUG: %s\n", fmt.Sprintf(format, args...))
	}
}

// get is "get [-in file] [-out file]".
func (r *dhRun) get() error {
	o := r.o

	var (
		data []byte
		err  error
		desc = "DH parameters"
		src  = "'" + o.In + "'"
	)

	if o.HasIn {
		data, err = r.readFile(o.In)
	} else {
		desc, src = certDHParamAttr, "LDAP"
		data, err = r.fromLDAP()
	}

	if err != nil {
		return err
	}

	if len(data) == 0 {
		return fmt.Errorf("no %s in %s", desc, src)
	}

	pemData, err := dhCheckPEM(data)
	if err != nil {
		return fmt.Errorf("check %s in %s failed: %w", desc, src, err)
	}

	// The parameters of LDAP are announced as written; those of a file that
	// are copied are not (the script said nothing then).
	return r.emit(pemData, !o.HasIn)
}

// fromLDAP is zimbraSSLDHParam of the global config, empty when it is unset.
func (r *dhRun) fromLDAP() ([]byte, error) {
	r.debug("getting '%s' from the global config", certDHParamAttr)

	gc, err := certGlobalConfig(r.e)
	if err != nil {
		return nil, fmt.Errorf("reading %s from LDAP: %w", certDHParamAttr, err)
	}

	return []byte(gc[certDHParamAttr]), nil
}

// readFile reads file, stdin for "-".
func (r *dhRun) readFile(file string) ([]byte, error) {
	if file == "-" {
		data, err := io.ReadAll(r.stdin)
		if err != nil {
			return nil, fmt.Errorf("read stdin failed: %w", err)
		}

		return data, nil
	}

	data, err := r.e.Sys.FS.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read '%s' failed: %w", file, err)
	}

	return data, nil
}

// emit writes the parameters of "get" or "set -out" to -out, stdout when it
// is not given or "-"; announce says that a file written is told.
func (r *dhRun) emit(data []byte, announce bool) error {
	o := r.o

	if !o.HasOut || o.Out == "-" {
		if _, err := r.stdout.Write(data); err != nil {
			return fmt.Errorf("write to stdout failed: %w", err)
		}

		return nil
	}

	if announce {
		over := ""
		if dhExists(r.e, o.Out) {
			over = "(over)"
		}

		r.say("%swriting params to '%s'", over, o.Out)
	}

	if err := dhWriteInPlace(r.e, o.Out, data); err != nil {
		return fmt.Errorf("create '%s' failed: %w", o.Out, err)
	}

	return nil
}

// dhWriteInPlace writes data to name as open(">") did: the file is truncated
// and written where it is, so /dev/stdout, a symlink or a file of another owner
// stays what it is.
func dhWriteInPlace(e *Env, name string, data []byte) error {
	f, err := e.Sys.FS.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close()

		return err
	}

	return f.Close()
}

func dhExists(e *Env, name string) bool {
	_, err := e.Sys.FS.Stat(name)

	return err == nil
}

// set is "set <-new [bits]|-in file> [-out file]".
func (r *dhRun) set() error {
	o := r.o

	var (
		data []byte
		err  error
		desc = certDHParamAttr
		src  = "the RFC 7919 group"
	)

	if o.HasIn {
		desc, src = "DH parameters", "'"+o.In+"'"

		data, err = r.readFile(o.In)
	} else {
		r.debug("using the RFC 7919 group of %d bits", o.Bits)

		data, err = dhGroupPEM(o.Bits)
	}

	if err != nil {
		return err
	}

	if len(data) == 0 {
		return fmt.Errorf("no %s in %s", desc, src)
	}

	pemData, err := dhCheckPEM(data)
	if err != nil {
		return fmt.Errorf("check %s from %s failed: %w", desc, src, err)
	}

	if o.HasOut {
		return r.emit(pemData, true)
	}

	r.say("saving '%s' to LDAP", certDHParamAttr)

	if err := r.saveToLDAP(pemData); err != nil {
		return fmt.Errorf("save to LDAP failed: %w", err)
	}

	return nil
}

// saveToLDAP stores data in zimbraSSLDHParam: a binary attribute, which
// "mcf" reads from a file.
func (r *dhRun) saveToLDAP(data []byte) error {
	f, err := r.e.Sys.FS.CreateTemp("", "zmdhparam-*.pem")
	if err != nil {
		return err
	}

	defer func() { _ = r.e.Sys.FS.Remove(f.Name()) }()

	_, err = io.Copy(f, bytes.NewReader(data))
	if cerr := f.Close(); err == nil {
		err = cerr
	}

	if err != nil {
		return err
	}

	// zmprov, which gets the commands that are not written to LDAP, runs as
	// zextras: the parameters are public, so anybody may read them.
	if err := r.e.Sys.FS.Chmod(f.Name(), 0o644); err != nil {
		return err
	}

	return r.e.Prov("mcf", certDHParamAttr, f.Name())
}
