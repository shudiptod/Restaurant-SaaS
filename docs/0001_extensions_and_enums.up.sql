CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TYPE platform_role AS ENUM ('superadmin', 'support');
CREATE TYPE account_status AS ENUM ('active', 'locked', 'suspended', 'canceled');
CREATE TYPE subscription_status AS ENUM ('trialing','active','past_due','canceled');
CREATE TYPE payment_status AS ENUM ('pending','completed','failed','refunded');
CREATE TYPE restaurant_status AS ENUM ('active', 'locked');
CREATE TYPE restaurant_role AS ENUM ('owner', 'admin');
CREATE TYPE order_status AS ENUM ('open','closed','cancelled');
