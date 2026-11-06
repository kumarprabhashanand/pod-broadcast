# The build stage runs on the build machine and cross-compiles, so multi-arch builds need no emulation.
FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY api api
COPY cmd/controller cmd/controller
COPY internal internal
ARG TARGETOS TARGETARCH VERSION
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -X github.com/kumarprabhashanand/pod-broadcast/internal/version.version=${VERSION}" -o /controller ./cmd/controller

FROM gcr.io/distroless/static:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329
COPY --from=build /controller /controller
ENTRYPOINT ["/controller"]
