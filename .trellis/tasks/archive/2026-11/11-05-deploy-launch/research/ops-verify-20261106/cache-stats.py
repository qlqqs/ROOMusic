from drilldb import *
import sys
q='''if [ -d /data/media-cache ]; then find /data/media-cache -type f -name '*.flac' -exec stat -c '%s' '{}' ';' | awk '{n++;s+=$1} END {printf "{\\"flac_files\\":%d,\\"flac_bytes\\":%d}\\n",n,s}'; else echo '{"flac_files":0,"flac_bytes":0}'; fi'''
result=json.loads(out(D+['exec','-T','app','/bin/sh','-c',q]));save(sys.argv[1],result)
