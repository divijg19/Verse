-- Migration: create poems table
CREATE TABLE IF NOT EXISTS poems (
  id UUID PRIMARY KEY,
  content TEXT NOT NULL,
  created_at TIMESTAMP DEFAULT now()
);
CREATE TABLE poems (
  id UUID PRIMARY KEY,
  content TEXT NOT NULL,
  created_at TIMESTAMP DEFAULT now()
);