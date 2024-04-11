FROM golang:1.22-bookworm as build-env
WORKDIR /go/src/webpush-fcm-relay

COPY go.mod ./
COPY go.sum ./
RUN go mod download

COPY *.go ./
RUN go build -o webpush-fcm-relay

FROM gcr.io/distroless/base-debian12
COPY --from=build-env /go/src/webpush-fcm-relay/webpush-fcm-relay /

ARG GIT_REPOSITORY_URL
ARG GIT_COMMIT_SHA
ARG VERSION
ENV DD_GIT_REPOSITORY_URL=${GIT_REPOSITORY_URL}
ENV DD_GIT_COMMIT_SHA=${GIT_COMMIT_SHA}
ENV DD_VERSION=${VERSION}

EXPOSE 5985

ENTRYPOINT [ "/webpush-fcm-relay", "-bind=0.0.0.0:5985" ]
