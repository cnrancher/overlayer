
FROM registry.suse.com/bci/golang:1.25 AS build

WORKDIR /app

# Pre-download dependencies
RUN --mount=type=cache,target=/go/pkg/mod/ \
    --mount=type=bind,source=go.sum,target=go.sum \
    --mount=type=bind,source=go.mod,target=go.mod \
    go mod download -x

# Build the binary
ENV GOCACHE=/root/.cache/go-build
ARG TAG=v0.1.0-alpha
ARG COMMIT=unknown
RUN --mount=type=cache,target=/go/pkg/mod/ \
    --mount=type=cache,target="/root/.cache/go-build" \
    --mount=type=bind,target=. \
    CGO_ENABLED=0 \
    go build \
    -buildmode=pie \
    -ldflags="-extldflags='-static' -s -w -X github.com/cnrancher/overlayer/pkg/utils.Version=${TAG} -X github.com/cnrancher/overlayer/pkg/utils.Commit=${COMMIT}" \
    -o /usr/local/bin/proxy \
    ./pkg/cmd
RUN proxy version

FROM registry.suse.com/bci/python:3.12 AS server

COPY --from=build /usr/local/bin/proxy /usr/local/bin/
COPY config.example.yaml /config.yaml
COPY static /static

# Run Registry Reverse Proxy Server
ENTRYPOINT [ "proxy" ]
CMD [ "run", "-c=/config.yaml" ]
