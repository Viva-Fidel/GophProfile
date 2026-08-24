package domain

import "errors"

var (
	// ErrNotFound — аватарка не найдена или мягко удалена.
	ErrNotFound = errors.New("avatar not found")
	// ErrForbidden — операция запрещена для текущего пользователя.
	ErrForbidden = errors.New("forbidden")
	// ErrInvalidFormat — файл не является JPEG, PNG или WebP.
	ErrInvalidFormat = errors.New("invalid file format")
	// ErrTooLarge — файл больше MaxUploadBytes.
	ErrTooLarge = errors.New("file too large")
	// ErrMissingFile — в запросе нет файла.
	ErrMissingFile = errors.New("file is required")
	// ErrMissingUser — не передан идентификатор пользователя.
	ErrMissingUser = errors.New("user id is required")
	// ErrInvalidSize — недопустимый query-параметр size.
	ErrInvalidSize = errors.New("invalid size")
	// ErrInvalidFormatParam — недопустимый query-параметр format.
	ErrInvalidFormatParam = errors.New("invalid format")
)
