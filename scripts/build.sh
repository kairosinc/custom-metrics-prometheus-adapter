#!/bin/bash
set -euf -o pipefail
set -o xtrace

PUSH=${1:-""}
PROJECT=kairosinc/custom-metrics-prometheus-adapter
if [[ $PUSH != "" ]]; then
    PROJECT='quay.io/kairosinc/custom-metrics-prometheus-adapter'
fi
BRANCH=${GIT_BRANCH#*/}
ROOT="$( cd "$( dirname "${BASH_SOURCE[0]}" )/.." >/dev/null && pwd )"

if [[ ${GIT_BRANCH#*}/ == "/" && ${GIT_COMMIT:0:7} == "" ]]; then
	TAG=latest
else
	TAG=${GIT_BRANCH#*/}-${GIT_COMMIT:0:7}${BUILD_NUMBER}
fi

# Do Docker build
echo "Building image"
docker build -t $PROJECT:${TAG} -f ${ROOT}/Dockerfile ${ROOT}

if [[ $PUSH == "" ]]; then
    exit 0
fi

# CI/CD environment
echo "Logging into Quay.io..."
docker login -u="${QUAY_USERNAME}" -p="${QUAY_PASSWORD}" quay.io || true

echo "Pushing image"
docker push $PROJECT:${TAG}

# Push Docker image to latest tag if on master branch
if [[ ${GIT_BRANCH#*/} == "master" ]]; then
	docker tag $PROJECT:${TAG} $PROJECT:latest
	docker push $PROJECT:latest
fi

# Remove local image
echo "Removed images:"
if [[ $(docker images $PROJECT:${TAG} |wc -l ) > 1 ]]; then
  docker rmi -f $PROJECT:${TAG}
fi

if [[ ${GIT_BRANCH#*/} == "master" ]]; then
	docker rmi -f $PROJECT:latest
fi