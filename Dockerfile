FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /quorum ./cmd/quorum

FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json ./
RUN npm install
COPY web/ .
RUN npm run build

FROM alpine:3.22
RUN apk add --no-cache iputils ca-certificates && \
    addgroup -S uptime && adduser -S uptime -G uptime
WORKDIR /app
COPY --from=build /quorum ./quorum
COPY --from=web /web/dist ./web/dist
COPY services.yaml ./services.yaml
RUN chown -R uptime:uptime /app
USER uptime
EXPOSE 8080
ENTRYPOINT ["./quorum"]
CMD ["serve", "-static", "web/dist"]
