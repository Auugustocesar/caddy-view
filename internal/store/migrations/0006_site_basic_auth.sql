-- A managed site may ask for a username and a password. The password is never stored: the hash is bcrypt, and the
-- user and the hash are both set or both empty. A site that existed before keeps neither.
ALTER TABLE sites ADD COLUMN basic_auth_user TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN basic_auth_hash TEXT NOT NULL DEFAULT '';
