# This file was modified by Cozero GmbH in 2026.

FROM golang:1.27-alpine AS builder
WORKDIR /src
RUN apk --no-cache add git
COPY *.go go.mod go.sum ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/prometheus-ecs-discovery .

FROM alpine:latest AS runtime
RUN apk --no-cache add ca-certificates
COPY --from=builder /bin/prometheus-ecs-discovery /bin/
ENTRYPOINT ["prometheus-ecs-discovery"]
