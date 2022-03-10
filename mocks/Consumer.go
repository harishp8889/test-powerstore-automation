// Code generated by mockery. DO NOT EDIT.

package mocks

import (
	array "github.com/dell/csi-powerstore/pkg/array"
	fs "github.com/dell/csi-powerstore/pkg/common/fs"

	mock "github.com/stretchr/testify/mock"
)

// Consumer is an autogenerated mock type for the Consumer type
type Consumer struct {
	mock.Mock
}

// Arrays provides a mock function with given fields:
func (_m *Consumer) Arrays() map[string]*array.PowerStoreArray {
	ret := _m.Called()

	var r0 map[string]*array.PowerStoreArray
	if rf, ok := ret.Get(0).(func() map[string]*array.PowerStoreArray); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(map[string]*array.PowerStoreArray)
		}
	}

	return r0
}

// DefaultArray provides a mock function with given fields:
func (_m *Consumer) DefaultArray() *array.PowerStoreArray {
	ret := _m.Called()

	var r0 *array.PowerStoreArray
	if rf, ok := ret.Get(0).(func() *array.PowerStoreArray); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*array.PowerStoreArray)
		}
	}

	return r0
}

// SetArrays provides a mock function with given fields: _a0
func (_m *Consumer) SetArrays(_a0 map[string]*array.PowerStoreArray) {
	_m.Called(_a0)
}

// SetDefaultArray provides a mock function with given fields: _a0
func (_m *Consumer) SetDefaultArray(_a0 *array.PowerStoreArray) {
	_m.Called(_a0)
}

// UpdateArrays provides a mock function with given fields: _a0, _a1
func (_m *Consumer) UpdateArrays(_a0 string, _a1 fs.Interface) error {
	ret := _m.Called(_a0, _a1)

	var r0 error
	if rf, ok := ret.Get(0).(func(string, fs.Interface) error); ok {
		r0 = rf(_a0, _a1)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}