FROM golang:1.27.4-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS build
WORKDIR /src
COPY main.go .
RUN go mod init echo && CGO_ENABLED=0 go build -trimpath -ldflags=-s -o /echo .

FROM scratch
COPY --from=build /echo /echo
USER 65532:65532
ENTRYPOINT ["/echo"]
