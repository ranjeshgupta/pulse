FROM node:24-alpine AS ui
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pulse ./cmd/pulse

FROM alpine:3.22
RUN adduser -D -H pulse
COPY --from=build /out/pulse /usr/local/bin/pulse
USER pulse
EXPOSE 7070
ENTRYPOINT ["/usr/local/bin/pulse"]
