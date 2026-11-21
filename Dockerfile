FROM golang:1.27 AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /sepay-mcp .

FROM gcr.io/distroless/static-debian12:nonroot
LABEL io.modelcontextprotocol.server.name="io.github.QDuyPhan/sepay-mcp"
COPY --from=build /sepay-mcp /sepay-mcp
# stdio: docker run -i --rm -e SEPAY_API_TOKEN=... image
# HTTP:  docker run -p 8080:8080 image -http :8080
ENTRYPOINT ["/sepay-mcp"]
