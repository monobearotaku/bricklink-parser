FROM golang:1.25rc1-alpine AS build

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .

RUN go build -o bricklink-parser /app/cmd/main

FROM alpine:3.19.0 AS runner

WORKDIR /app
COPY --from=build /app/bricklink-parser /app/bricklink-parser
COPY --from=build /app/config.yaml /app/config.yaml

CMD ["/app/bricklink-parser"]  
