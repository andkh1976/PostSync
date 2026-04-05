FROM golang:1.24-alpine AS builder

ARG MAX_TOKEN
ARG TG_TOKEN
ARG DATABASE_URL
ARG POSTGRES_PASSWORD
ARG POSTGRES_DB
ARG POSTGRES_USER
ARG MINI_APP_URL
ARG WEBHOOK_URL
ARG LOG_LEVEL
ARG ALLOWED_USERS

RUN apk add --no-cache ca-certificates

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go mod tidy
RUN CGO_ENABLED=0 go build -o /postsync .

FROM alpine:3.21

ARG MAX_TOKEN
ARG TG_TOKEN
ARG DATABASE_URL
ARG POSTGRES_PASSWORD
ARG POSTGRES_DB
ARG POSTGRES_USER
ARG MINI_APP_URL
ARG WEBHOOK_URL
ARG LOG_LEVEL
ARG ALLOWED_USERS

RUN apk add --no-cache ca-certificates
WORKDIR /app

COPY --from=builder /postsync /usr/local/bin/postsync
COPY --from=builder /src/frontend ./frontend

ENTRYPOINT ["postsync"]
