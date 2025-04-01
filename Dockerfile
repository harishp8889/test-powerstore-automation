# Copyright © 2023-2025 Dell Inc. or its subsidiaries. All Rights Reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#      http://www.apache.org/licenses/LICENSE-2.0
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# some arguments that must be supplied
ARG GOIMAGE
ARG BASEIMAGE

# Stage to build the driver
FROM $GOIMAGE as builder

WORKDIR /workspace
COPY . .

RUN go generate ./cmd/csi-powerstore
RUN GOOS=linux CGO_ENABLED=0 go build -o csi-powerstore ./cmd/csi-powerstore

# Stage to build the driver image
FROM $BASEIMAGE
WORKDIR /
LABEL vendor="Dell Technologies" \
      maintainer="Dell Technologies" \
      name="csi-powerstore" \
      summary="CSI Driver for Dell EMC PowerStore" \
      description="CSI Driver for provisioning persistent storage from Dell EMC PowerStore" \
      release="1.14.0" \
      version="2.14.0" \
      license="Apache-2.0"
COPY licenses /licenses

# validate some cli utilities are found
RUN which mkfs.ext4
RUN which mkfs.xfs
RUN echo "export PATH=$PATH:/sbin:/bin" > /etc/profile.d/ubuntu_path.sh

COPY --from=builder /workspace/csi-powerstore /
ENTRYPOINT ["/csi-powerstore"]
