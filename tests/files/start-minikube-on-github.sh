#!/usr/bin/env bash

for i in 0 1; do fallocate -l 1G loop${i} ; sudo losetup -f loop${i}; sudo losetup -a ; done
