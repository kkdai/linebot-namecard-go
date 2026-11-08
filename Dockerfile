FROM golang:1.27-bookworm AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /linebot-namecard-go ./cmd/server

FROM gcr.io/distroless/static-debian12

COPY --from=builder /linebot-namecard-go /linebot-namecard-go

EXPOSE 8080
ENTRYPOINT ["/linebot-namecard-go"]
