-- 在 search-scale.sql 之上，将第二版本拆为独立专辑，并给只有 missing 曲目的发行补一首在场曲目。
INSERT INTO release_groups(id)
SELECT ('20000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid
FROM generate_series(8001,10000) i;

UPDATE releases SET group_id=('20000000-0000-4000-8000-'||right(id::text,12))::uuid
WHERE id BETWEEN '30000000-0000-4000-8000-000000008001' AND '30000000-0000-4000-8000-000000010000';

INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,
 source_status,observed_at,source_kind,codec,duration_seconds,bit_depth,sample_rate,channels)
SELECT ('51000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,m.id,1,'补充在场曲目 '||i,r.artist,1,
 r.source_root_id,'subsonic-scale-'||i||'.flac','present',NOW(),'flac_vorbis_comment','flac',180,16,44100,2
FROM generate_series(9901,10000) i
JOIN media m ON m.id=('40000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid
JOIN releases r ON r.id=m.release_id;
