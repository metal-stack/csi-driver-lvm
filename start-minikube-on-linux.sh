#!/usr/bin/env bash

minikube start --memory 5g
minikube ssh 'for i in 0 1; do fallocate -l 1G loop${i} ; sudo losetup -f loop${i}; sudo losetup -a ; done'
minikube ssh 'sudo rm /sbin/losetup'
scp -o 'StrictHostKeyChecking=no' -i $(minikube ssh-key) $(which losetup)  docker@$(minikube ip):/tmp/losetup
minikube ssh 'sudo mv /tmp/losetup /sbin/losetup'
