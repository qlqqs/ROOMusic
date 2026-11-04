-- 只在独立测试 schema 使用；十万 present、一万 missing、一万发行、八千组。
INSERT INTO library_roots(id,path,status,revision) VALUES('10000000-0000-4000-8000-000000000001','/synthetic-scale','disabled',1);
INSERT INTO release_groups(id) SELECT ('20000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid FROM generate_series(1,8000) i;
INSERT INTO releases(id,group_id,title,artist,album_artist,source_root_id,relative_directory,candidate_kind,label,catalog_number,year)
SELECT ('30000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,
 ('20000000-0000-4000-8000-'||lpad((CASE WHEN i<=8000 THEN i ELSE i-8000 END)::text,12,'0'))::uuid,
 '合成专辑 '||lpad(i::text,5,'0'),CASE WHEN i<=5000 THEN '周杰倫' ELSE 'Album Artist '||i END,
 CASE WHEN i<=5000 THEN '周杰倫' ELSE 'Album Artist '||i END,
 '10000000-0000-4000-8000-000000000001','release-'||i,'ordinary_directory',
 CASE WHEN i<=5000 THEN '热门厂牌' ELSE '厂牌 '||i END,'SRCL-'||i,1980+i%40
FROM generate_series(1,10000) i;
INSERT INTO media(id,release_id,position,title)
SELECT ('40000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,
 ('30000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,1,'CD 1' FROM generate_series(1,10000) i;
INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,codec,duration_seconds,bit_depth,sample_rate,channels)
SELECT ('50000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,
 ('40000000-0000-4000-8000-'||lpad((CASE WHEN i<=100000 THEN 1+(i-1)%9900 ELSE 9901+(i-100001)%100 END)::text,12,'0'))::uuid,
 i,CASE WHEN i%97=0 THEN '爱 ' ELSE '' END || '交响曲 第'||lpad(i::text,5,'0')||'号' || CASE WHEN i%101=0 THEN ' %_~' ELSE '' END,
 CASE WHEN i<=50000 THEN '周杰倫 / Artist '||i%20000 ELSE 'Artist '||i%20000||' / 演奏家 '||(i+1)%20000 END,
 1,'10000000-0000-4000-8000-000000000001','track-'||i||'.flac',CASE WHEN i<=100000 THEN 'present' ELSE 'missing' END,
 NOW(),'flac_vorbis_comment','flac',180,16,44100,2 FROM generate_series(1,110000) i;
INSERT INTO track_credits(track_id,role,name,position)
SELECT t.id,role,CASE role WHEN 'composer' THEN CASE WHEN t.position<=50000 THEN '久石譲' ELSE '作曲者 '||t.position%20000 END
 WHEN 'conductor' THEN '指挥者 '||t.position%20000 WHEN 'performer' THEN '演奏家 '||t.position%20000
 ELSE '制作人 '||t.position%20000 END,r.position
FROM tracks t CROSS JOIN (VALUES('composer',1),('conductor',2),('performer',3),('producer',4)) r(role,position);
