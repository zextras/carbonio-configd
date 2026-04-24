// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

library(
    identifier: 'jenkins-lib-common@1.6.2',
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
    agent {
        node {
            label 'golang-v1'
        }
    }

    environment {
        GOPRIVATE = 'gitlab.com/zextras,bitbucket.org/zextras,github.com/zextras'
    }

    options {
        buildDiscarder(logRotator(numToKeepStr: '25'))
        skipDefaultCheckout()
        timeout(time: 2, unit: 'HOURS')
    }

    parameters {
        booleanParam defaultValue: false,
            description: 'Set to true to skip the test stage',
            name: 'SKIP_TEST'
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

        stage('Tests') {
            when { expression { params.SKIP_TEST != true } }
            steps {
                container('golang') {
                    sh 'go run gotest.tools/gotestsum@latest --format testname --junitfile tests.xml -- -coverprofile=coverage.out ./...'
                    junit allowEmptyResults: false, checksName: 'Tests', testResults: 'tests.xml'
                }
            }
        }

        stage('SonarQube analysis') {
            steps {
                container('golangci-lint') {
                    script {
                        scannerHome = tool 'SonarScanner'
                    }
                    sh 'golangci-lint run ./... --issues-exit-code 0 --output.checkstyle.path linter.out'
                }
                withSonarQubeEnv(credentialsId: 'sonarqube-user-token',
                    installationName: 'SonarQube instance') {
                    sh "${scannerHome}/bin/sonar-scanner"
                }
            }
        }

        stage('Build Packages') {
            steps {
                echo 'Building deb/rpm packages'
                buildStage([
                    buildDirs: ['build'],
                    prepare: true,
                    prepareFlags: ' -g ',
                    rockySinglePkg: true,
                    ubuntuSinglePkg: true,
                ])
            }
        }

        stage('Upload artifacts') {
            when {
                expression { return uploadStage.shouldUpload() }
            }
            tools {
                jfrog 'jfrog-cli'
            }
            steps {
                uploadStage(
                    packages: yapHelper.resolvePackageNames('build/yap.json'),
                    rockySinglePkg: true,
                    ubuntuSinglePkg: true,
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
