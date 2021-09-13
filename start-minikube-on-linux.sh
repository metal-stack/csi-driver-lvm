#!/usr/bin/env bash

minikube start --memory 5g --driver kvm2
minikube ssh 'for i in 0 1; do fallocate -l 1G loop${i} ; sudo losetup -f loop${i}; sudo losetup -a ; done'
