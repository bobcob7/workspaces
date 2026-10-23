import { createAsyncThunk } from "@reduxjs/toolkit";
import type { AppError } from "./api";
import type { RootState, ThunkExtra } from "./store";

export const createAppThunk = createAsyncThunk.withTypes<{
  state: RootState;
  extra: ThunkExtra;
  rejectValue: AppError;
}>();
