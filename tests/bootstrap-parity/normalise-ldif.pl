#!/usr/bin/perl
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only
#
# Normalise one or more slapcat LDIF files (stdin/args) so that two bootstraps
# of the same configuration compare equal:
#  - drops operational/volatile attributes (entryCSN, entryUUID, timestamps,
#    modifiersName, creatorsName, contextCSN, ...),
#  - replaces every UUID by <id:DN-of-the-entry-owning-it> (zimbraId and
#    friends keep their cross-references, but not their random value),
#  - replaces secrets (password hashes, keys, certificates, tokens, syncrepl
#    credentials) by "<secret>",
#  - replaces generalized-time values by "<time>",
#  - sorts entries by DN and attributes/values within an entry.
use strict;
use warnings;

# Attributes removed entirely.
my %DROP = map { lc($_) => 1 } qw(
  entryCSN entryUUID createTimestamp modifyTimestamp modifiersName creatorsName
  contextCSN structuralObjectClass hasSubordinates numSubordinates
  zimbraCreateTimestamp zimbraLastLogonTimestamp zimbraAuthTokens
);

# Attribute names whose value is a secret or key material.
my $SECRET_ATTR = qr/(?:(?:password|passwd|pass|pw)$|^olcrootpw$|privatekey|publickey|certificate|certauthority|dhparam|sslcert
  |^zimbraauthtokenkey$|^zimbracsrftokenkey$|secret|^zimbradkimselector$|^zimbradkimprivatekey$)/xi;

local $/ = undef;
my $input = join('', <>);
$input =~ s/\r//g;
$input =~ s/\n //g;    # LDIF line folding

# Parse into entries: [dn, [[attr, value], ...]]
my @entries;
for my $block (split /\n\n+/, $input) {
    my @attrs;
    my $dn;
    for my $line (split /\n/, $block) {
        next if $line =~ /^#/ || $line eq '';
        my ($name, $sep, $val) = $line =~ /^([^:]+):(:?)\s?(.*)$/ or next;
        if ($sep eq ':') {
            # base64: decode when printable text so that diffs are readable.
            require MIME::Base64;
            my $dec = MIME::Base64::decode_base64($val);
            $val = $dec if $dec =~ /^[\x20-\x7e\n\t]*$/;
            $val = "{b64}$val" if $dec !~ /^[\x20-\x7e\n\t]*$/;
        }
        if (lc($name) eq 'dn') { $dn = $val; next; }
        push @attrs, [ $name, $val ];
    }
    next unless defined $dn;
    push @entries, [ $dn, \@attrs ];
}

# galsync.<random password>@domain is generated at bootstrap (zmgsautil createAccount).
sub galnorm {
    my ($v) = @_;
    $v =~ s/galsync\.[a-z0-9]+/galsync.<random>/g;
    return $v;
}

# Pass 1: UUID -> owning DN (zimbraId / olc ids).
my %owner;
for my $e (@entries) {
    my ($dn, $attrs) = @$e;
    for my $a (@$attrs) {
        $owner{ lc $a->[1] } = galnorm($dn) if lc($a->[0]) eq 'zimbraid';
    }
}
my $UUID = qr/[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}/;
sub scrub {
    my ($v) = @_;
    $v = galnorm($v);
    $v =~ s/($UUID)/exists $owner{lc $1} ? '<id:' . $owner{lc $1} . '>' : '<uuid>'/ge;
    return $v;
}

my @out;
for my $e (@entries) {
    my ($dn, $attrs) = @$e;
    my @lines;
    for my $a (@$attrs) {
        my ($name, $val) = @$a;
        next if $DROP{ lc $name };
        if ($name =~ $SECRET_ATTR) {
            # Presence marker only (value, and the hash algorithm tag, are volatile).
            $val = length($val) ? '<secret>' : '<empty>';
        }
        else {
            $val =~ s/\bcredentials=\S+/credentials=<secret>/g;
            $val =~ s/^\d{14}(?:\.\d+)?Z$/<time>/;
            $val = scrub($val);
        }
        $val =~ s/\n/\\n/g;    # multi-line values (PEM) stay one sortable line
        push @lines, "$name: $val";
    }
    push @out, [ scrub($dn), join("\n", sort @lines) ];
}
print map { "dn: $_->[0]\n$_->[1]\n\n" } sort { $a->[0] cmp $b->[0] } @out;
