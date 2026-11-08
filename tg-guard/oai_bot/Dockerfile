FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /oai_bot ./cmd/oai_bot

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /oai_bot /app/oai_bot
ENV TZ=Asia/Shanghai
ENTRYPOINT ["/app/oai_bot", "-c", "/app/config.yaml"]
