// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

// The texts of pod2usage for zmcertmgr, as Pod::Usage prints them for the pod
// of the script: "-help" the synopsis and the options (verbose 1), "-man" the
// whole page (verbose 2), a refused command line the synopsis only (verbose 0).

// certSynopsis is the SYNOPSIS section.
//
//nolint:lll // the text of the pod
const certSynopsis = `Usage:
    zmcertmgr <command> [options] [-help|-man] [-debug [#]]

      -help     displays a usage synopsis with information about options
      -man      displays the complete documentation

    Where valid command/option combinations are:

      createca  [-new [-newkey]] [-keysize keysize] [-digest digest] [-subject subject]
      createcrt [-new] [-keysize keysize] [-digest digest] [-subject subject] [-days days] [-subjectAltNames host1,host2] [-allservers] [-noDefaultSubjectAltName]
      deployca  [-localonly]

      createcsr     <self|comm> [-new] [-keysize keysize] [-digest digest] [-subject subject] [-subjectAltNames host1,host2] [-noDefaultSubjectAltName]
      deploycrt   <<self>|<comm [certfile ca_chain_file]>> [-allservers] [-localonly] [[-deploy $services] ...]
      getcrt        <self|comm> [-allservers]
      savecrt       <self|comm> [-allservers]
      viewcsr       <self|comm> [csr_file]
      viewstagedcrt <self|comm> [certfile]
      verifycrt     <self|comm> [[[priv_key] [certfile]] [ca_chain_file]]

      verifycrtchain <ca_chain_file> <certfile>
      verifycrtkey   <priv_key>      <certfile>

      viewdeployedcrt    [all|ldap|mailboxd|mta|proxy]
      checkcrtexpiration [all|ldap|mailboxd|mta|proxy] [-days days]
      addcacert <certfile>
      migrate

     <certfile> <csr_file> <priv_key> are stored by "type" as follows:
       self       /opt/zextras/ssl/carbonio/server/server.{crt,csr,key}
       commercial /opt/zextras/ssl/carbonio/commercial/commercial.{crt,csr,key}

`

// certOptionsText is the OPTIONS section.
//
//nolint:lll // the text of the pod
const certOptionsText = `Options:
  Shared Options:
    -allservers
        The effect of this option varies depending on the command. Reference
        a specific command to determine the purpose of this argument.

    -days days
        The number of days to certify a certificate for, except when used
        with the checkcrtexpiration command. Default is 1825 days (~5
        years). For checkcrtexpiration a default of 30 days is used.

    -digest algorithm
        The digest algorithm to be used. Default is sha256. The following
        are valid: ripemd160,sha,sha1,sha224,sha256,sha384,sha512.

    -keysize keysize
        The RSA keysize in bits, for example "-keysize 4096". Minimum
        keysize is 2048. Default keysize is 2048.

    -localonly
        Avoids updating any certificate related settings in LDAP.

    -new
        Force the generation of a new CA/Cert/CSR, overwriting existing
        data.

        For createca, the additional argument of -newkey can be used to
        force the creation of a new private key for the CA. If the -newkey
        option is used on an existing install, it will break the established
        trust of the existing CA. Once the existing CA is replaced by a new
        CA, via deployca, TLS communications will fail due to the broken
        trust relationship between existing certificates and the replaced
        CA. A temporary workaround to this situation is to set
        ssl_allow_untrusted_certs=true via zmlocalconfig.

    -noDefaultSubjectAltName
        Disable the (default) inclusion of the current zmhostname in the
        subjectAltNames of a Cert/CSR.

    services
    -deploy services
        The set of services to be used for a request. The service names can
        be specified as comma separate values or the -deploy argument can be
        used multiple times.

        Valid services are 'all' or any of: ldap,mailboxd,mta,proxy.

    -subject subject
        The X.500 distinguished name (DN). The default is:

         CA:
          /O=CA/OU=Zextras Carbonio/CN=` + "`" + `zmhostname` + "`" + `
         Server:
               /OU=Zextras Carbonio/CN=` + "`" + `zmhostname` + "`" + `

        This argument is passed as the '-subj' argument to openssl req and
        thus must be formatted as /type0=value0/type1=value1/type2=...,
        characters may be escaped by \ (backslash), no spaces are skipped.

        Commonly used attributes include:

          String X.500 AttributeType
          ------ -------------------
          C      countryName
          ST     stateOrProvinceName
          L      localityName
          O      organizationName
          OU     organizationalUnitName
          CN     commonName

        For example:

          /C=US/ST=NC/L=Mayberry/O=Sales/OU=Example, Inc./CN=www.example.com

        Note: the DN of the CA (issuer) must be different than the DN of the
        server (subject) to avoid MS windows based clients from throwing a
        SSL error similar to, "This certificate has invalid digital
        signature."

    -subjectAltNames subjectAltNames
        Additional host names that may use the certificate other than the
        one listed in the subject. The altername names can be specified as
        comma separate values or the -subjectAltNames can be used multiple
        times.

    -debug [#]
        Enable verbose output. Verbosity can be increased by specifying an
        optional (integer) debug level. Often useful for troubleshooting.

    -help
        Display a usage synopsis with information about options and exit.

    -man
        Display the complete documentation and exit.

  Self-Signed Certificate Options:
    createca
        Generates a Certificate Authority (CA).

    deployca
        Deploys a Certificate Authority (CA).

    createcrt
        Creates a self-signed certificate based on a CSR generated using
        createcsr. By default the certificate subjectAltNames contains the
        current zmhostname unless the -noDefaultSubjectAltName argument is
        used.

        For createcrt, the use of -allservers will set the CR
        SubjectAltNames to contain the names of all servers in the Carbonio
        deployment (zmprov gas).

  Self-Signed and Commercial Certificate Options:
    addcacert
        Appends an otherwise untrusted ssl certificate to the cacerts file.
        This is primarily for allowance of untrusted ssl certificates in
        external data sources.

    checkcrtexpiration
        Check if certificate(s) expire within -days days.

    createcsr
        Creates a certificate signing request (CSR) for either a self or
        commercially signed certificate authority. By default the CSR
        subjectAltNames contains the current zmhostname unless the
        -noDefaultSubjectAltName argument is used.

    getcrt
    savecrt
        For getcrt and savecrt, the use of -allservers causes the
        configuration keys to be get/set as a global
        (getConfig/modifyConfig) configuration settings
        (zimbraSSLCertificate and zimbraSSLPrivateKey) instead of as a
        per-server setting (getServer/modifyServer).

    deploycrt
        Deploys a certificate.

        For deploycrt, the use of -allservers will cause zmcertmgr to
        iterate through all servers in the Carbonio deployment (zmprov gas,
        minus the initiating zmcertmgr host). On each server, the following
        two commands are run:

          $ zmcertmgr getcrt $type -allservers; zmcertmgr deploycrt $type

        Certificates are installed from:

          self - /opt/zextras/ssl/carbonio/server
          comm - /opt/zextras/ssl/carbonio/commercial

        Note: if -allservers does not work, there may be problems with SSH
        authorized keys in the environment.

    viewcsr
        Shows a certificate signing request (CSR).

    verifycrt
        Combines verifycrtkey and verifycrtchain verification (see below).

    verifycrtkey
        Compares private key and certificate modulus digests.

    verifycrtchain
        Verifies a certificate chain.

    viewdeployedcrt
        Shows a deployed certificate on the local server.

    viewstagedcrt
        Shows a staged certificate. A staged certificate is placed in a
        staging file, where all files that will be deployed with the
        certificate are kept. You can use the staging area to verify that
        you are ready to deploy a certificate.

    migrate

`

// certManText is the page "-man" shows.
//
//nolint:lll // the text of the pod
const certManText = `NAME
    zmcertmgr - certificate management tool for Carbonio

SYNOPSIS
    zmcertmgr <command> [options] [-help|-man] [-debug [#]]

      -help     displays a usage synopsis with information about options
      -man      displays the complete documentation

    Where valid command/option combinations are:

      createca  [-new [-newkey]] [-keysize keysize] [-digest digest] [-subject subject]
      createcrt [-new] [-keysize keysize] [-digest digest] [-subject subject] [-days days] [-subjectAltNames host1,host2] [-allservers] [-noDefaultSubjectAltName]
      deployca  [-localonly]

      createcsr     <self|comm> [-new] [-keysize keysize] [-digest digest] [-subject subject] [-subjectAltNames host1,host2] [-noDefaultSubjectAltName]
      deploycrt   <<self>|<comm [certfile ca_chain_file]>> [-allservers] [-localonly] [[-deploy $services] ...]
      getcrt        <self|comm> [-allservers]
      savecrt       <self|comm> [-allservers]
      viewcsr       <self|comm> [csr_file]
      viewstagedcrt <self|comm> [certfile]
      verifycrt     <self|comm> [[[priv_key] [certfile]] [ca_chain_file]]

      verifycrtchain <ca_chain_file> <certfile>
      verifycrtkey   <priv_key>      <certfile>

      viewdeployedcrt    [all|ldap|mailboxd|mta|proxy]
      checkcrtexpiration [all|ldap|mailboxd|mta|proxy] [-days days]
      addcacert <certfile>
      migrate

     <certfile> <csr_file> <priv_key> are stored by "type" as follows:
       self       /opt/zextras/ssl/carbonio/server/server.{crt,csr,key}
       commercial /opt/zextras/ssl/carbonio/commercial/commercial.{crt,csr,key}

OPTIONS
  Shared Options
    -allservers
        The effect of this option varies depending on the command. Reference
        a specific command to determine the purpose of this argument.

    -days days
        The number of days to certify a certificate for, except when used
        with the checkcrtexpiration command. Default is 1825 days (~5
        years). For checkcrtexpiration a default of 30 days is used.

    -digest algorithm
        The digest algorithm to be used. Default is sha256. The following
        are valid: ripemd160,sha,sha1,sha224,sha256,sha384,sha512.

    -keysize keysize
        The RSA keysize in bits, for example "-keysize 4096". Minimum
        keysize is 2048. Default keysize is 2048.

    -localonly
        Avoids updating any certificate related settings in LDAP.

    -new
        Force the generation of a new CA/Cert/CSR, overwriting existing
        data.

        For createca, the additional argument of -newkey can be used to
        force the creation of a new private key for the CA. If the -newkey
        option is used on an existing install, it will break the established
        trust of the existing CA. Once the existing CA is replaced by a new
        CA, via deployca, TLS communications will fail due to the broken
        trust relationship between existing certificates and the replaced
        CA. A temporary workaround to this situation is to set
        ssl_allow_untrusted_certs=true via zmlocalconfig.

    -noDefaultSubjectAltName
        Disable the (default) inclusion of the current zmhostname in the
        subjectAltNames of a Cert/CSR.

    services
    -deploy services
        The set of services to be used for a request. The service names can
        be specified as comma separate values or the -deploy argument can be
        used multiple times.

        Valid services are 'all' or any of: ldap,mailboxd,mta,proxy.

    -subject subject
        The X.500 distinguished name (DN). The default is:

         CA:
          /O=CA/OU=Zextras Carbonio/CN=` + "`" + `zmhostname` + "`" + `
         Server:
               /OU=Zextras Carbonio/CN=` + "`" + `zmhostname` + "`" + `

        This argument is passed as the '-subj' argument to openssl req and
        thus must be formatted as /type0=value0/type1=value1/type2=...,
        characters may be escaped by \ (backslash), no spaces are skipped.

        Commonly used attributes include:

          String X.500 AttributeType
          ------ -------------------
          C      countryName
          ST     stateOrProvinceName
          L      localityName
          O      organizationName
          OU     organizationalUnitName
          CN     commonName

        For example:

          /C=US/ST=NC/L=Mayberry/O=Sales/OU=Example, Inc./CN=www.example.com

        Note: the DN of the CA (issuer) must be different than the DN of the
        server (subject) to avoid MS windows based clients from throwing a
        SSL error similar to, "This certificate has invalid digital
        signature."

    -subjectAltNames subjectAltNames
        Additional host names that may use the certificate other than the
        one listed in the subject. The altername names can be specified as
        comma separate values or the -subjectAltNames can be used multiple
        times.

    -debug [#]
        Enable verbose output. Verbosity can be increased by specifying an
        optional (integer) debug level. Often useful for troubleshooting.

    -help
        Display a usage synopsis with information about options and exit.

    -man
        Display the complete documentation and exit.

  Self-Signed Certificate Options
    createca
        Generates a Certificate Authority (CA).

    deployca
        Deploys a Certificate Authority (CA).

    createcrt
        Creates a self-signed certificate based on a CSR generated using
        createcsr. By default the certificate subjectAltNames contains the
        current zmhostname unless the -noDefaultSubjectAltName argument is
        used.

        For createcrt, the use of -allservers will set the CR
        SubjectAltNames to contain the names of all servers in the Carbonio
        deployment (zmprov gas).

  Self-Signed and Commercial Certificate Options
    addcacert
        Appends an otherwise untrusted ssl certificate to the cacerts file.
        This is primarily for allowance of untrusted ssl certificates in
        external data sources.

    checkcrtexpiration
        Check if certificate(s) expire within -days days.

    createcsr
        Creates a certificate signing request (CSR) for either a self or
        commercially signed certificate authority. By default the CSR
        subjectAltNames contains the current zmhostname unless the
        -noDefaultSubjectAltName argument is used.

    getcrt
    savecrt
        For getcrt and savecrt, the use of -allservers causes the
        configuration keys to be get/set as a global
        (getConfig/modifyConfig) configuration settings
        (zimbraSSLCertificate and zimbraSSLPrivateKey) instead of as a
        per-server setting (getServer/modifyServer).

    deploycrt
        Deploys a certificate.

        For deploycrt, the use of -allservers will cause zmcertmgr to
        iterate through all servers in the Carbonio deployment (zmprov gas,
        minus the initiating zmcertmgr host). On each server, the following
        two commands are run:

          $ zmcertmgr getcrt $type -allservers; zmcertmgr deploycrt $type

        Certificates are installed from:

          self - /opt/zextras/ssl/carbonio/server
          comm - /opt/zextras/ssl/carbonio/commercial

        Note: if -allservers does not work, there may be problems with SSH
        authorized keys in the environment.

    viewcsr
        Shows a certificate signing request (CSR).

    verifycrt
        Combines verifycrtkey and verifycrtchain verification (see below).

    verifycrtkey
        Compares private key and certificate modulus digests.

    verifycrtchain
        Verifies a certificate chain.

    viewdeployedcrt
        Shows a deployed certificate on the local server.

    viewstagedcrt
        Shows a staged certificate. A staged certificate is placed in a
        staging file, where all files that will be deployed with the
        certificate are kept. You can use the staging area to verify that
        you are ready to deploy a certificate.

    migrate

EXAMPLES
  Single-Node Self-Signed Certificate
    1.  Begin by generating a new Certificate Authority (CA).

         $ zmcertmgr createca -new

    2.  Then generate a certificate signed by the CA that expires in 365
        days.

         $ zmcertmgr createcrt -new -days 365

    3.  Deploy the certificate.

         $ zmcertmgr deploycrt self

    4.  Deploy the CA.

         $ zmcertmgr deployca

    5.  To finish, verify the certificate was deployed to all the services.

         $ zmcertmgr viewdeployedcrt

  Multi-Node Self-Signed Certificate
    1.  Begin by generating a new Certificate Authority (CA).

         $ zmcertmgr createca -new
         $ zmcertmgr deployca

    2.  Then generate a certificate, signed by the CA, that expires in 1825
        days with either wild-card or subject altnames.

         $ zmcertmgr createcrt -new -days 1825 -subjectAltNames "*.example.com"
         $ zmcertmgr createcrt -new -days 1825 -subject "/C=US/ST=CA/O=Example/CN=*.example.com"
         $ zmcertmgr createcrt -new -days 1825 -subjectAltNames "host1.example.com,host2.example.com"

    3.  Deploy the certificate to all nodes in the deployment.

         $ zmcertmgr deploycrt self -allservers

    4.  To finish, verify the certificate was deployed.

         $ zmcertmgr viewdeployedcrt

SEE ALSO
    RFC 5280
        <https://tools.ietf.org/html/rfc5280> - Internet X.509 Public Key
        Infrastructure Certificate and Certificate Revocation List (CRL)
        Profile

    RFC 4514
        <https://tools.ietf.org/html/rfc4514> - LDAP String Representation
        of Distinguished Names

CAVEATS
`
