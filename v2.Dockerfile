FROM node:22-alpine AS frontend
WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
RUN npm run build

FROM golang:1.23-alpine AS backend
RUN apk add --no-cache gcc musl-dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/web/build internal/embed/static/
RUN CGO_ENABLED=1 go build -ldflags "-s -w" -o flowcase ./cmd/flowcase

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=backend /app/flowcase /usr/local/bin/flowcase
EXPOSE 8080
ENTRYPOINT ["flowcase"]
