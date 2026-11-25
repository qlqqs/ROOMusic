FROM alpine:3.23@sha256:f3d55900449227ae9e237ede521e7b665d53a253b42065ba90b9db9275e198ad AS package
# Alpine 3.23 仓库没有 squeezelite。用固定的上游源码生成本地 APK。
RUN apk add --no-cache build-base alsa-lib-dev flac-dev libvorbis-dev mpg123-dev
ARG SQUEEZELITE_COMMIT
ARG SQUEEZELITE_MICRO
ADD https://codeload.github.com/ralph-irving/squeezelite/tar.gz/${SQUEEZELITE_COMMIT} /src.tar.gz
RUN mkdir /src && tar -xzf /src.tar.gz --strip-components=1 -C /src \
    && grep -Fx "#define MICRO_VERSION \"$SQUEEZELITE_MICRO\"" /src/squeezelite.h \
    && make -C /src -j2 OPTS="-DLINKALL -DNO_FAAD -DNO_MAD" \
    && install -Dm755 /src/squeezelite /pkg/usr/bin/squeezelite \
    && strip /pkg/usr/bin/squeezelite \
    && apk mkpkg --files /pkg --output /squeezelite.apk \
       --info name:squeezelite --info "version:2.0.0_p${SQUEEZELITE_MICRO}-r0" --info arch:$(apk --print-arch) \
       --info license:GPL-3.0-or-later --info 'depends:alsa-lib flac-libs libvorbis mpg123-libs'

FROM alpine:3.23@sha256:f3d55900449227ae9e237ede521e7b665d53a253b42065ba90b9db9275e198ad
RUN --mount=from=package,source=/squeezelite.apk,target=/squeezelite.apk \
    apk add --no-cache --allow-untrusted /squeezelite.apk
ENTRYPOINT ["/usr/bin/squeezelite"]
