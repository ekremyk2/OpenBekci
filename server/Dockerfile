# syntax=docker/dockerfile:1
# Build context: the repository root (go.work, sdk/, packs/example/, packs/tr/ and server/ are needed):
#   docker build -f server/Dockerfile -t openbekci .

FROM golang:1.27 AS build
WORKDIR /src
COPY go.work go.work.sum ./
COPY sdk/go.mod sdk/go.sum* sdk/
COPY packs/example/go.mod packs/example/go.sum* packs/example/
COPY packs/tr/go.mod packs/tr/go.sum* packs/tr/
COPY server/go.mod server/go.sum server/
RUN cd server && go mod download
COPY sdk/ sdk/
COPY packs/example/ packs/example/
COPY packs/tr/ packs/tr/
COPY server/ server/
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
# -tags nowebassets until the web build is wired into the image.
RUN cd server && CGO_ENABLED=0 go build -trimpath -tags nowebassets \
      -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
      -o /out/openbekci ./cmd/openbekci

RUN mkdir /data-skel

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/openbekci /usr/local/bin/openbekci
# /data must exist and be owned by nonroot (65532) before VOLUME, so a fresh
# named volume inherits writable ownership.
COPY --from=build --chown=65532:65532 /data-skel /data
ENV OPENBEKCI_DATA_DIR=/data
VOLUME /data
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/openbekci"]
CMD ["serve"]
