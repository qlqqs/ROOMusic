package postgres

const beforeUPnPMigration = `
INSERT INTO users(id,username,password_hash)
VALUES ('f6900000-0000-4000-8000-000000000001','upnp-migration','test-only');
INSERT INTO playback_devices(id,user_id,kind,name,volume)
VALUES ('f6900000-0000-4000-8000-000000000002','f6900000-0000-4000-8000-000000000001','browser','原有播放器',0.75);
INSERT INTO playback_zones(id,user_id,device_id,position_seconds)
VALUES ('f6900000-0000-4000-8000-000000000003','f6900000-0000-4000-8000-000000000001','f6900000-0000-4000-8000-000000000002',12);
INSERT INTO library_roots(id,path) VALUES ('f6900000-0000-4000-8000-000000000004','/synthetic/upnp-migration');
INSERT INTO release_groups(id) VALUES ('f6900000-0000-4000-8000-000000000005');
INSERT INTO releases(id,group_id,title,artist)
VALUES ('f6900000-0000-4000-8000-000000000006','f6900000-0000-4000-8000-000000000005','合成发行','合成艺人');
INSERT INTO media(id,release_id,position)
VALUES ('f6900000-0000-4000-8000-000000000007','f6900000-0000-4000-8000-000000000006',1);
INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,observed_at)
VALUES ('f6900000-0000-4000-8000-000000000008','f6900000-0000-4000-8000-000000000007',1,'合成曲目','合成艺人',
 'f6900000-0000-4000-8000-000000000004','synthetic.flac',NOW());
INSERT INTO playback_queue_entries(zone_id,entry_id,position,track_id)
VALUES ('f6900000-0000-4000-8000-000000000003','f6900000-0000-4000-8000-000000000009',0,'f6900000-0000-4000-8000-000000000008');`

const upnpDiscoveryRollbackFixture = `
UPDATE upnp_settings SET enabled=true,shared=false WHERE id=1;
INSERT INTO upnp_manual_devices(udn,address,name,model,capabilities)
VALUES ('uuid:upnp-migration','http://192.168.10.2/description.xml','合成设备','合成型号','{"pause":true}');
INSERT INTO upnp_device_grants(udn,user_id)
VALUES ('uuid:upnp-migration','f6900000-0000-4000-8000-000000000001'),
 ('uuid:upnp-autodiscovered','f6900000-0000-4000-8000-000000000001');`

const upnpPlaybackRollbackFixture = `
INSERT INTO playback_devices(id,user_id,kind,name,external_id,max_sample_rate)
VALUES ('f7000000-0000-4000-8000-000000000002','f6900000-0000-4000-8000-000000000001','upnp','新建局域网设备','uuid:upnp-migration',96000);
INSERT INTO playback_zones(id,user_id,device_id,position_seconds)
VALUES ('f7000000-0000-4000-8000-000000000003','f6900000-0000-4000-8000-000000000001','f7000000-0000-4000-8000-000000000002',24);
INSERT INTO playback_queue_entries(zone_id,entry_id,position,track_id)
VALUES ('f7000000-0000-4000-8000-000000000003','f7000000-0000-4000-8000-000000000009',0,'f6900000-0000-4000-8000-000000000008');`
