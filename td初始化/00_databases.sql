-- New deployment only. 12 months is represented as a fixed 365 days.
-- IF NOT EXISTS does not change retention or schema of an existing database.
-- Do not run DROP DATABASE. Source 171 has WAL_LEVEL 0; new installs use WAL_LEVEL 2.
CREATE DATABASE IF NOT EXISTS `kayex_data`
  KEEP 365d DURATION 1d COMP 2
  VGROUPS 20 BUFFER 256 PAGES 256 PAGESIZE 4
  MAXROWS 40000 MINROWS 1000 WAL_LEVEL 2 STT_TRIGGER 1
  CACHEMODEL 'last_row' PRECISION 'ms';
CREATE DATABASE IF NOT EXISTS `sop_hardware`
  KEEP 365d DURATION 1d COMP 2 MAXROWS 40000 MINROWS 1000
  WAL_LEVEL 2 PRECISION 'ms';
