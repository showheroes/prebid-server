@Library('shRelease@try-fetching-tags') _

def dcr = "dcr.jenkins:5000"
def dockerImageName = "prebid-server"
def gar_locations = ["us", "europe", "asia"]
def gar_repo = "viralize-143916/monetize"
def releaseConfig = [
    "default_release": "minor"
]
def buildImage(name, dcr) {
    sh """
        docker build --network host \
        --cache-to type=inline \
        -t ${dcr}/${name}:${GIT_COMMIT} \
        -t ${dcr}/${name}:${CURRENT_BRANCH} \
        --cache-from ${dcr}/${name}:latest \
        --cache-from ${dcr}/${name}:${CURRENT_BRANCH} \
        --cache-from ${dcr}/${name}:${SRC_BRANCH} \
        --push .
    """
    if (env.CURRENT_BRANCH == 'release') {
        sh "crane tag ${dcr}/${name}:${GIT_COMMIT} latest --insecure"
    }
}

pipeline {
    agent { label "jnlp_dind_v2" }
    environment {
        CURRENT_BRANCH = "${env.CHANGE_BRANCH ?: env.BRANCH_NAME}"
        GITHUB_TOKEN = credentials('SHBOT_GITHUB_RELEASE_TOKEN')
        SRC_BRANCH = shRelease.getPullRequestInfo().head.ref.trim()
        RELEASE_TAG = shRelease.getReleaseTag(releaseConfig)
    }

    stages {
        stage('Build image') {
            steps {
                script {
                    buildImage(dockerImageName, dcr)
                }
            }
        }

        stage('Release Candidate') {
            when {
                allOf {
                    changeRequest();
                }
            }
            steps {
                script {
                    shRelease.releaseCandidate(env.RELEASE_TAG)
                    shRelease.copyDockerImage(
                        source_image: "${dcr}/${dockerImageName}:${GIT_COMMIT}",
                        target_image: dockerImageName,
                        gar_locations: gar_locations,
                        gar_repo: gar_repo,
                        target_tags: [env.RELEASE_TAG]
                    )
                }
            }
        }
        stage('Release Stable') {
            when {
                allOf {
                    branch 'release'
                }
            }
            steps {
                script {
                    shRelease.releaseStable(env.RELEASE_TAG)
                    shRelease.copyDockerImage(
                        source_image: "${dcr}/${dockerImageName}:${GIT_COMMIT}",
                        target_image: dockerImageName,
                        gar_locations: gar_locations,
                        gar_repo: gar_repo,
                        target_tags: [env.RELEASE_TAG, 'latest']
                    )
                }
            }
        }
    }
}
