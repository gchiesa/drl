# 001-bootstrap-empty-app-and-testing-framework.md

This milestone is aimed to bootstrap an empty DRL app and the required manual testing framework

## GOAL 

We should be able to run `docker-compose up` and see services starting. 
DRL will be just a simple empty application that does nothing.
DRL is not started in the docker-compose yet.
Envoy passthroughs all the requests to echo-server
Echo server is based on `https://hub.docker.com/r/mccutchen/go-httpbin`
k6s starts a test with ramp-up mode to the echo-server with the defaults:
- duration: 60seconds 
- request per seconds: 10
- number of virtual clients: 10
- ramp-up: 10%/5sec-30%/10sec-50%/15sec-100%/30sec
