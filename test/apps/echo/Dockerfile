FROM golang:1.27.3-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
WORKDIR /src
COPY main.go .
RUN go mod init echo && CGO_ENABLED=0 go build -trimpath -ldflags=-s -o /echo .

FROM scratch
COPY --from=build /echo /echo
USER 65532:65532
ENTRYPOINT ["/echo"]
