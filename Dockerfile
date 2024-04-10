FROM golang:1.22-bookworm as build-env
WORKDIR /go/src/webpush-fcm-relay

COPY go.mod ./
COPY go.sum ./
RUN go mod download

COPY *.go ./
RUN go build -o webpush-fcm-relay

FROM gcr.io/distroless/base
COPY --from=build-env /go/src/webpush-fcm-relay/webpush-fcm-relay /

EXPOSE 5985

ENTRYPOINT [ "/webpush-fcm-relay", "-bind=0.0.0.0:5985" ]
