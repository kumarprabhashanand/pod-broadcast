FROM golang:1.27.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY api api
COPY cmd/controller cmd/controller
COPY internal internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags=-s -o /controller ./cmd/controller

FROM gcr.io/distroless/static:nonroot
COPY --from=build /controller /controller
ENTRYPOINT ["/controller"]
