FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /sitewatch ./cmd/sitewatch

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && adduser -D -H sitewatch
COPY --from=build /sitewatch /usr/local/bin/sitewatch
RUN mkdir /data && chown sitewatch /data
USER sitewatch
ENV SITEWATCH_DB=/data/sitewatch.db
VOLUME ["/data"]
ENTRYPOINT ["sitewatch"]
CMD ["watch"]
