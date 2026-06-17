FROM golang:1.25.10-alpine AS build-stage
RUN apk update && apk upgrade && apk add build-base

WORKDIR /camino-messenger-bot

# add ext library
RUN apk add olm-dev

# build
COPY . .
RUN apk --no-cache add git bash grep curl jq
RUN if git rev-parse --git-dir >/dev/null 2>&1; then git submodule update --init; fi

ARG CAMINO_BOT_COMMIT
ARG CAMINO_BOT_TAG
ENV CAMINO_BOT_COMMIT=$CAMINO_BOT_COMMIT
ENV CAMINO_BOT_TAG=$CAMINO_BOT_TAG

RUN CAMINOBOT_PATH=$(pwd) bash ./scripts/build.sh


#runtime stage
FROM alpine:3.21 AS runtime-stage

RUN apk add --no-cache olm-dev

WORKDIR /

COPY --from=build-stage /camino-messenger-bot/build/camino-messenger-bot /camino-messenger-bot

ENTRYPOINT ["./camino-messenger-bot"]
