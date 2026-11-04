// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

library(
    identifier: 'jenkins-lib-common@v5.3.1',
    retriever: modernSCM([
        $class: 'GitSCMSource',
        credentialsId: 'jenkins-integration-with-github-account',
        remote: 'git@github.com:zextras/jenkins-lib-common.git',
    ])
)

properties(defaultPipelineProperties())

boolean isCommitTagged() {
    return env.GIT_TAG ? true : false
}

pipeline {
    // Same pod as before (JCasC template 'golang-v1') plus the 'reuse' sidecar that
    // dt3_reuseCompliance requires (the image dt3_pipeline adds). merge() is mandatory:
    // the default override() would drop fields declared only in the parent template.
    agent {
        kubernetes {
            inheritFrom 'golang-v1'
            yamlMergeStrategy merge()
            yaml '''\
spec:
  containers:
    - name: reuse
      image: fsfe/reuse:5.0.2-debian
      command: [sleep]
      args: ["99999999"]
      workingDir: /home/jenkins/agent
      tty: true
'''
        }
    }

    environment {
        GOPRIVATE = 'gitlab.com/zextras,bitbucket.org/zextras,github.com/zextras'
        // gotestsum and govulncheck are pinned in tools/go.mod (as are deadcode and dupl).
        GOTESTSUM = 'go tool -modfile=tools/go.mod gotestsum'
    }

    options {
        buildDiscarder(logRotator(numToKeepStr: '25'))
        skipDefaultCheckout()
        timeout(time: 2, unit: 'HOURS')
    }

    // The nightly build of the primary branch runs the checks and the LDAP
    // integration suite only: it never builds, publishes or uploads (see the
    // triggeredBy 'TimerTrigger' guards below).
    triggers {
        cron(env.BRANCH_IS_PRIMARY == 'true' ? 'H 3 * * *' : '')
    }

    parameters {
        booleanParam defaultValue: false,
            description: 'Set to true to skip the test stage',
            name: 'SKIP_TEST'
        booleanParam defaultValue: false,
            description: 'Run the OpenLDAP container integration suite (make test-integration-ldap) on a docker-in-docker pod. Always runs on the nightly build.',
            name: 'RUN_LDAP_INTEGRATION'
    }

    stages {
        stage('Setup') {
            steps {
                checkout scm
                script {
                    gitMetadata()
                }
            }
        }

        stage('REUSE Lint') {
            steps {
                dt3_reuseCompliance()
            }
        }

        stage('THIRDPARTIES') {
            when {
                anyOf {
                    changeRequest()
                    expression { env.BRANCH_IS_PRIMARY == 'true' }
                }
            }
            steps {
                dt3_thirdparties()
            }
        }

        // vet for every build-tag combination (integration is compiled, never run),
        // unreachable code, duplication ratchet, and drift of the tables generated
        // from carbonio-mailbox LC.java (hermetic: the inputs are vendored in scripts/lc-source).
        // Mirrors `make check`'s static part; reproduce locally with
        // `make vet-tags deadcode dup generate-check`.
        stage('Static checks') {
            steps {
                container('golang') {
                    // The workspace is owned by the jnlp user; git (dup-check) refuses it as root otherwise.
                    sh 'git config --global --add safe.directory "$PWD"'
                    sh 'make vet-tags deadcode dup generate-check'
                }
            }
        }

        // The default tags run with the race detector and feed Sonar's coverage; the
        // profiling/tracing variants compile different files, so they are tested
        // (and their coverage reported) separately.
        stage('Tests') {
            when { expression { params.SKIP_TEST != true } }
            steps {
                container('golang') {
                    sh '$GOTESTSUM --format testname --junitfile tests.xml -- -race -coverprofile=coverage.out ./...'
                    sh '$GOTESTSUM --format testname --junitfile tests-tracing.xml -- -race -tags tracing -coverprofile=coverage.tracing.out ./...'
                    sh '$GOTESTSUM --format testname --junitfile tests-profiling.xml -- -race -tags profiling -coverprofile=coverage.profiling.out ./...'
                    sh '$GOTESTSUM --format testname --junitfile tests-full.xml -- -race -tags "tracing profiling" -coverprofile=coverage.full.out ./...'
                    junit allowEmptyResults: false, checksName: 'Tests', testResults: 'tests*.xml'
                }
            }
        }

        stage('Vulnerability scan') {
            when { expression { params.SKIP_TEST != true } }
            steps {
                container('golang') {
                    sh 'make security'
                }
            }
        }

        // One golangci-lint run writes both the checkstyle report Sonar reads and the
        // console output, and its exit status fails the build after Sonar has the report.
        stage('Lint and SonarQube analysis') {
            steps {
                script {
                    String scannerHome = tool 'SonarScanner'
                    int lintStatus = 0
                    container('golangci-lint') {
                        lintStatus = sh(returnStatus: true,
                            script: 'golangci-lint run ./... --output.checkstyle.path linter.out --output.text.path stdout')
                    }
                    withSonarQubeEnv(credentialsId: 'sonarqube-user-token',
                        installationName: 'SonarQube instance') {
                        sh "${scannerHome}/bin/sonar-scanner"
                    }
                    if (lintStatus != 0) {
                        error "golangci-lint failed (exit status ${lintStatus})"
                    }
                }
            }
        }

        // The pod templates golang-v1 and base have no container runtime, so this runs
        // `make test-integration-ldap` in a golang container started by the dockerd
        // sidecar of the docker-build-v1 pod (the docker CLI binary and socket of the
        // sidecar are mounted into it: the suite skips itself without a reachable
        // docker). Nightly on the primary branch, or on demand with RUN_LDAP_INTEGRATION.
        stage('LDAP integration') {
            when {
                beforeAgent true
                anyOf {
                    triggeredBy 'TimerTrigger'
                    expression { params.RUN_LDAP_INTEGRATION == true }
                }
            }
            agent {
                kubernetes {
                    inheritFrom 'docker-build-v1'
                    yamlMergeStrategy merge()
                }
            }
            steps {
                checkout scm
                container('dind') {
                    sh 'sh scripts/ci-ldap-integration.sh'
                }
            }
        }

        stage('Build Packages') {
            when { not { triggeredBy 'TimerTrigger' } }
            steps {
                echo 'Building deb/rpm packages'
                buildStage([
                    buildDirs: ['build'],
                    parallelBuilds: true,
                    prepare: true,
                    prepareFlags: ' -g ',
                ])
                buildStage([
                    architecture: 'aarch64',
                    buildDirs: ['build'],
                    distros: ['ubuntu-jammy'],
                    prepare: true,
                    prepareFlags: ' -g ',
                ])
            }
        }

        // The carbonio-configd image: the static binary plus the carbonio-core
        // templates (docker/configd/Dockerfile). It needs a docker-capable node, which
        // the golang pod is not.
        stage('Publish docker image') {
            when {
                beforeAgent true
                not { triggeredBy 'TimerTrigger' }
            }
            agent {
                node {
                    label 'base'
                }
            }
            steps {
                checkout scm
                gitMetadata()
                script {
                    // The image carries the version of the package it ships with.
                    String pkgver = sh(
                        script: '''awk -F= '/^pkgver=/{gsub(/"/, "", $2); print $2}' build/configd/PKGBUILD''',
                        returnStdout: true,
                    ).trim()
                    dockerStage([
                        dockerfile: 'docker/configd/Dockerfile',
                        imageName: 'carbonio-configd',
                        ocLabels: [
                            title: 'Carbonio configd',
                            descriptionFile: 'docker/configd/description.md',
                            vendor: 'Zextras',
                            version: pkgver,
                        ],
                        platforms: ['linux/amd64', 'linux/arm64'] as Set,
                    ])
                }
            }
        }

        stage('Upload artifacts') {
            when {
                allOf {
                    not { triggeredBy 'TimerTrigger' }
                    expression { return uploadStage.shouldUpload() }
                }
            }
            tools {
                jfrog 'jfrog-cli'
            }
            steps {
                uploadStage(
                    yapPath: 'build/yap.json',
                )
                uploadStage(
                    architecture: 'aarch64',
                    distros: ['ubuntu-jammy'],
                    yapPath: 'build/yap.json',
                )
            }
        }
    }

    post {
        always {
            emailext attachLog: true,
                body: '$DEFAULT_CONTENT',
                recipientProviders: [requestor()],
                subject: '$DEFAULT_SUBJECT',
                to: env.GIT_COMMIT_EMAIL
        }
    }
}
