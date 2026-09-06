FROM golang:1.27-alpine

RUN apk add --no-cache ffmpeg && go install github.com/air-verse/air@latest

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

EXPOSE 8080

CMD ["air", "-c", ".air.toml"]
