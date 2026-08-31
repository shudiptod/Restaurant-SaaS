DROP POLICY IF EXISTS tenant_isolation ON restaurant_users;

CREATE POLICY tenant_isolation ON restaurant_users
  USING (user_id = current_setting('app.current_user_id')::uuid);
