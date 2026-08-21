FROM --platform=$BUILDPLATFORM golang:1.26-trixie AS build-env
WORKDIR /go/src/webpush-fcm-relay

ARG TARGETOS
ARG TARGETARCH

COPY go.mod ./
COPY go.sum ./
RUN go mod download

COPY *.go ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o webpush-fcm-relay

FROM gcr.io/distroless/base-debian13
COPY --from=build-env /go/src/webpush-fcm-relay/webpush-fcm-relay /

ARG GIT_REPOSITORY_URL
ARG GIT_COMMIT_SHA
ARG VERSION
ENV DD_GIT_REPOSITORY_URL=${GIT_REPOSITORY_URL}
ENV DD_GIT_COMMIT_SHA=${GIT_COMMIT_SHA}
ENV DD_VERSION=${VERSION}

EXPOSE 5985

ENTRYPOINT [ "/webpush-fcm-relay", "-bind=0.0.0.0:5985" ]
