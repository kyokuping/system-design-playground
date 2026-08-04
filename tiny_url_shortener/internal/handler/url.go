package handler

import "net/url"

type URLService interface {
	GetShortURL(userID string, longURL *url.URL) (string, error)
	GetURLMapping(shortKey string) (*url.URL, error)
	GetLongURL(shortKey string) (*url.URL, error)
}
