ALTER TABLE ONLY applications_users DROP CONSTRAINT applications_application_id_applications_id_foreign;
ALTER TABLE ONLY applications_users DROP CONSTRAINT applications_user_id_users_id_foreign;

DROP TABLE applications_users;
