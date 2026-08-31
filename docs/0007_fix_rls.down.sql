DROP POLICY IF EXISTS tenant_isolation ON restaurant_users;

CREATE POLICY tenant_isolation ON restaurant_users
  USING (restaurant_id IN (
    SELECT ru.restaurant_id FROM restaurant_users ru
    WHERE ru.user_id = current_setting('app.current_user_id')::uuid AND ru.status = 'active'
  ));
