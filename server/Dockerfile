# syntax=docker/dockerfile:1
# Build context: the repository root (go.work, sdk/ and server/ are needed):
#   docker build -f server/Dockerfile -t openbekci .

FROM golang:1.27 AS build
WORKDIR /src
COPY go.work go.work.sum ./
COPY sdk/go.mod sdk/go.sum* sdk/
COPY server/go.mod server/go.sum server/
RUN cd server && go mod download
COPY sdk/ sdk/
COPY server/ server/
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
# -tags nowebassets until the web build is wired into the image.
RUN cd server && CGO_ENABLED=0 go build -trimpath -tags nowebassets \
      -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
      -o /out/openbekci ./cmd/openbekci

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/openbekci /usr/local/bin/openbekci
ENV OPENBEKCI_DATA_DIR=/data
VOLUME /data
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/openbekci"]
CMD ["serve"]
