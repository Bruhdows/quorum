FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /quorum ./cmd/quorum

FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
RUN npm run build

FROM alpine:3.22
# ping needs CAP_NET_RAW for ICMP sockets. setcap bakes it into the binary
# so the non-root uptime user can run ping checks. The container still needs
# cap_add: [NET_RAW] at runtime (compose sets it on the agent).
# libcap-utils is just the package that ships the setcap binary.
RUN apk add --no-cache iputils ca-certificates libcap-utils wget && \
    for p in /bin/ping /usr/bin/ping; do if [ -f "$p" ]; then setcap cap_net_raw+p "$p"; fi; done && \
    addgroup -S uptime && adduser -S uptime -G uptime
WORKDIR /app
COPY --from=build /quorum ./quorum
COPY --from=web /web/dist ./web/dist
COPY services.yaml ./services.yaml
RUN chown -R uptime:uptime /app
USER uptime
EXPOSE 8080
# Same check as the compose healthcheck, for plain `docker run` users.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://localhost:8080/health || exit 1
ENTRYPOINT ["./quorum"]
CMD ["serve", "-static", "web/dist"]
