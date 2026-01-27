// Check out https://github.com/showheroes/jenkins-shared-libs
@Library('shRelease') _

def dockerImageName = "prebid-server"
def pushConfig = [
    "local_image": "$dockerImageName",
    "target_image": "$dockerImageName",
    "gar_locations": "europe,us,asia",
    "gar_repo": "viralize-143916/monetize"
]
def releaseLabel = "release-candidate" // <- new tag/images apply only to PR with this label
def releaseConfig = [
    "default_release": "minor"
]

pipeline {
    // Check out https://github.com/showheroes/jenkins-agents
    agent { label 'jnlp_dind_buildx' }
    environment {
        GITHUB_TOKEN = credentials('SHBOT_GITHUB_RELEASE_TOKEN')
        GCE_SERVICE_ACCOUNT_KEY = credentials('JENKINS_GCP_SA_KEY')
        RELEASE_TAG = shRelease.getReleaseTag(releaseConfig)
    }

    stages {
       stage('Docker Build') {
           steps {
               // Build the Docker image
               script {
                    def lastRelease = shRelease.getLastRelease()
                    echo "Last stable release is: $lastRelease"
                    def prID = shRelease.getPullRequestID()
                    echo "PR ID is: $prID"
                    sh "docker buildx build --network=host . -t ${dockerImageName}"
               }
           }
       }

        stage('Release Candidate') {
            when {
                allOf {
                    changeRequest();
                    expression { shRelease.prHasLabel(releaseLabel) }
                }
            }
            steps {
                script {
                    shRelease.releaseCandidate(env.RELEASE_TAG)
                    sh 'gcloud auth activate-service-account --key-file=${GCE_SERVICE_ACCOUNT_KEY}'
                    pushConfig["target_tags"] = env.RELEASE_TAG
                    shRelease.pushDockerImage(pushConfig)
                }
            }
        }
        stage('Release Stable') {
            when {
                allOf {
                    branch 'master'
                    expression { shRelease.prHasLabel(releaseLabel) }
                }
            }
            steps {
                script {
                    shRelease.releaseStable(env.RELEASE_TAG)
                    sh 'gcloud auth activate-service-account --key-file=${GCE_SERVICE_ACCOUNT_KEY}'
                    pushConfig["target_tags"] = "${env.RELEASE_TAG},latest"
                    shRelease.pushDockerImage(pushConfig)
                }
            }
        }
    }
}
