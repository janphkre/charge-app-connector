FROM golang:1.18-alpine
RUN apk add tzdata
RUN apk add android-tools

RUN mkdir -m 0750 /root/.android
ADD insecure_shared_adbkey /root/.android/adbkey
ADD insecure_shared_adbkey.pub /root/.android/adbkey.pub

WORKDIR /app

COPY go.mod ./
COPY main.go ./

RUN go mod tidy
RUN go build -o /car_connector

CMD [ "/car_connector" ]