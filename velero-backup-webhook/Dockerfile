# syntax=docker/dockerfile:1.7

FROM golang:1.22.5 AS build

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64 \
    GOFLAGS=-mod=readonly

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod,sharing=private \
    go mod download && go mod verify

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod,sharing=private \
    --mount=type=cache,target=/root/.cache/go-build,sharing=private \
    go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/admission-webhook .

FROM gcr.io/distroless/static-debian12:nonroot AS run

COPY --from=build --chown=65532:65532 --chmod=0555 /out/admission-webhook /usr/local/bin/admission-webhook

EXPOSE 443
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/admission-webhook"]
