CREATE TABLE poems (
  id UUID PRIMARY KEY,
  content TEXT NOT NULL,
  created_at TIMESTAMP DEFAULT now()
);