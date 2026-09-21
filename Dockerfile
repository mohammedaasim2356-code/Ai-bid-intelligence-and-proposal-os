FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /bidos .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D app
WORKDIR /app
COPY --from=build /bidos .
COPY evals ./evals
RUN mkdir storage && chown app storage
USER app
CMD ["./bidos", "serve"]
