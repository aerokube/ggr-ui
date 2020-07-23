#!/usr/bin/env bash

set -e

docker build -t $GITHUB_REPOSITORY .
docker tag $GITHUB_REPOSITORY $GITHUB_REPOSITORY:$1
docker login -u="$DOCKER_USERNAME" -p="$DOCKER_PASSWORD"
docker push $GITHUB_REPOSITORY
docker push $GITHUB_REPOSITORY:$1
