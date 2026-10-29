FROM golang:1.27.3-alpine AS build
WORKDIR /src
COPY main.go .
RUN go mod init echo && CGO_ENABLED=0 go build -trimpath -ldflags=-s -o /echo .

FROM scratch
COPY --from=build /echo /echo
USER 65532:65532
ENTRYPOINT ["/echo"]
